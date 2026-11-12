---
title: "Plan: repository binding for the github source, first slice"
date: 2026-11-12
status: accepted
stage: BUILT
next: "Parent: `just done` on the series, then one landing of Tasks 1 to 8 together; the host install and the owner's own config steps follow the landing and are not done here"
depends-on:
  - github-write-scopes.md
tags: [plan, github, broker, grants, config]
summary: "Hand-off for GW-D79's first slice as one series of eight commits: origin-only lowering, named repository tables, the scoped additive ceiling and its live check in the broker, stored-authority regression tests, the verbs, the migration, the refusal of the old key, and the docs. Every task carries its tests in full; none is published alone."
---

# Plan: repository binding for the github source, first slice

**Status:** 2026-11-12 (`0121997f1`). Tasks 1 to 8 are built as one series of commits and
verified; the series awaits its one landing. MEASURED: `just check-ci` and the completion census
at `0121997f1`, the full integration suite at `e0ad3eaa0` (production source identical to
`0121997f1`), and one nested launch on a build of `8a819c29e` that accepted a `[repos.<name>]`
table. UNMEASURED: native macOS, rootless reachability, a GitHub call under live authority,
agent delivery, and the hand-run CLI smoke of [Landing](#landing) step 3. The
[verification record](#verification-record) has the commands, counts and limits. Built here means
the code and its tests are in the series. It does not mean a release, a host install, or any
grant or config file changed.

**Baseline.** Written against `8cf2c548a`; `internal/` and `packs/` were byte-identical
to `45f576cb4` (`git diff --stat 45f576cb4 8cf2c548a` lists docs only). Completed from three
read-only packets, each checked against the tree at that commit, then revised on the lead's
integration guard and the config planner's closure addendum (run 25402925). Where a packet and
the source disagree, the source was followed and the report says where. The tasks below are kept
as they were handed off; line numbers in them are the baseline's.

**Design:** [`github-write-scopes.md`](github-write-scopes.md), [§8](github-write-scopes.md#8-which-repository-a-line-is-for),
[the first slice](github-write-scopes.md#current-support-and-the-repository-only-first-slice),
[GW-D79](github-write-scopes.md#GW-D79) and its bounds, [GW-D80](github-write-scopes.md#GW-D80).
The work kinds and the Git tables stay in [the seed](github-write-scopes-plan.md), still a sketch.

**Precedence.** The design wins on behavior. The tree wins on fact, for cosmetic drift only. This
file is advice. **Stop and report, do not commit**, when: a red test does not fail as printed; a
test marked *pin* is red before any production change; a named file is dirty; a named symbol is
gone; the fix needs a file outside the task's fence; a gate fails outside the task's scope.

**What was run before hand-off.** One red was observed, at `45f576cb4`:
`TestUnqualifiedGitHubRequestsLowerToOriginOnly`. Every other red and every pin in the tasks was,
at hand-off, predicted from reading the source, and the implementer confirmed each before
changing code. That is the baseline only. What was run on the built series is in the
[verification record](#verification-record).

**One series, one publication.** Task 1 alone narrows what a listed repository receives with no
spelling to ask for it back, so no task lands alone. All eight are commits on one branch in one
worktree, in order. The parent lands the branch once, after the gates under [Landing](#landing).
No release is cut between them.

**Scope.** This workspace's boundary is `mschulkind-oss/yolo-jail` only. No task edits a live
config file, a grant, an approval record, the broker store, or this repository's
`yolo-jail.toml` and `yolo-jail.local.toml`. No task runs `yolo request`, `yolo allow`,
`yolo config migrate` or `yolo config apply` outside a test fixture.

## Map

| Path | Change | Task |
| :--- | :--- | :--- |
| `internal/brokerscope/grant.go` | `RepoPattern`, `PatternRepoPrefix`, `String`, `ParseGrant`, `ValidGrant`, `GrantNote` | 2 |
| `internal/config/readableallow.go` | origin-only lowering in `ProduceReadableGateInput` (`~:717-736`); `repos` tables in `ResolveReadableAllowance` (`:207`); scoped ceiling at the gate stage; identity-aware run-time ceiling beside `BrokeredGrantKindsUsingReadablePolicy` (`:986`); `UserBrokerAuthorityOf` (`:1088`) carries scoped tables | 1, 2, 3 |
| `internal/config/workspacebrokergrants.go` | second caller narrows exactly as the gate does and projects named tables; a separate, explicit old-reach projection beside it for the migration's before side | 1, 2 |
| `internal/loopholedecl/brokered.go` and its TOML decode, `packs/github/loopholes/github-broker/loophole.toml` (`:39`) | the declared opt-in `unqualified_requests = "origin"` | 1 |
| `internal/config/schema.go` (`:238-274`), `inherit.go` (`:411`), `removals.go` (`:38`), `mountsrequest.go` (`:116`) | `repos.*` rows and the four censuses they feed | 2, 3 |
| `internal/config/readablescope.go`, `gateread.go`, `brokeredskipped.go` | `identityRequired` (`:41`), rows and provenance for `repos.<name>`, the path-string consumers, the contained-file walk | 2, 3 |
| `internal/config/reposcope_origin_test.go`, `reposcope_tables_test.go`, `reposcope_ceiling_test.go` | new | 1, 2, 3 |
| `internal/config/readabledispatch_test.go`, `readableapproverun_test.go` | rewrite | 1 |
| `internal/configmigrate/*_test.go` | fixtures name the remote `origin` | 1 |
| `internal/cli/run/brokeredscope_repos_test.go`, `brokeredscope_repobinding_test.go` | new | 2, 4 |
| `internal/cli/run/liveapply_test.go` | one new test | 4 |
| `internal/ghbroker/workingset.go` (`:76-114`), `scope.go` (`:71`), `runs.go` (`:83`), `approverun.go` (`:86`) | `userGrantCovers` asks the identity-aware ceiling; the four grant matches call `grantRepoCovers`, which admits a pattern only on a marked grant | 3 |
| `internal/ghbroker/repobinding_test.go` | new: fixtures, scoped ceiling (3), stored-authority pins (4) | 3, 4 |
| `internal/configmigrate/brokered.go` (`:41`) | user-authority entry carries scoped ceilings | 3 |
| `internal/cli/request.go`, `allow.go`, `configwhy.go`, `config_ref.txt` | the `repos` words, help, `config why`, config-ref | 5 |
| `internal/cli/requestrepos_test.go`, `allowrepos_test.go` | new | 5 |
| `internal/configmigrate/lower.go`, `plan.go`, `graph.go`, `brokered.go` (`:86`) | named-table and remote-table lowering, in-place TOML role, retained-write lines, the before side reads the old-reach projection | 6 |
| `internal/configmigrate/reposbinding_test.go`, `internal/cli/configmigraterepos_test.go` | new | 6 |
| `internal/config/renames.go` (`:53-56`), `removals.go` (`:80-81`), `readableallow.go` (`:513`), `internal/cli/request.go` (`:107`), `configwhy.go` (`:437`) | the refusal | 7 |
| `internal/ghbroker/scope.go:137`, `internal/gitbroker/broker.go:616`, `internal/brokerscope/requested.go:4`, `internal/setupcensus/configkeys.go:539`, `packs/github/briefing/gh.md:54`, `packs/github/README.md:61-73`, `internal/jailcontent/builtinskills/configuring-the-jail/SKILL.md:88`, `internal/cli/config_ref.txt` | text naming the old spelling | 7 |
| `userguide/guides/github.md`, `userguide/reference/cli-reference.md`, `configuration.md`, `settings-per-setup.md`, `docs/reference/git-broker.md`, this file, the design's Built column | docs | 8 |

Task 1 is `a7a3ce768`, on an isolated lane branch and not an ancestor of `main`. Its historical fixture comparator was changed to expect the new field; the frozen fixture is untouched.

## Reuse

- Gate fixtures, `internal/config/readablescope_test.go`: `readableScopeBroker`,
  `writeReadableScopeRemote`, `writeReadableScopeUser`, `readableScopeEntry`, `containsGrant`;
  `decode` in `config_test.go:17`. Consumer surface: `ReadWorkspaceForGate`, `NewScopeCheck`,
  `RecordApproval`, `ScopeChanges`.
- `brokerscope.ReadRemotes(workspace, host).Remotes`, each `{Name, Repo}`: only remotes on `host`
  are in it, so "origin absent" and "origin not on github.com" are one test.
- `brokerscope.Admits`, `ValidPattern`, `ValidRepo`, `WildcardNote`, `ValidGrantRepo`: the one
  matcher. No new glob.
- `mergeReadableConfigWalk` (`readablemerge.go:417`) already unions lists per table and lets a
  later scalar win. Project plus local `[repos.x]` needs no merge code.
- `compareScope` and `scopeBlock` (`scopeapproval.go:493`, `:605`) already key the record by
  identity. A retargeted name shows as one removal and one addition once the target has an
  `Entry` row. No comparison code.
- `outsideCeilingReason`, `ReadableRequestSkipped`, and the status rewrite at
  `readableallow.go:668-680`: the existing skip-and-report path, already at the gate stage.
- Verbs: `namedVerbEditUnless`, `requestConvertFirst`, `allowConvertFirst`, `configKeyComponent`,
  `parseConfigEditKey`, `config.EditTargetChoice{Kind: config.EditTargetLocal}`,
  `findDeclaredBroker`, `declaredCapabilities`, `requestGrantKind`, `hostApprovalStep`. The
  qualified verb runs the unqualified verb's capability switch with another key prefix.
- Migration: `markRemovedSetupKeys` → `RoleRespell` → `withRemovedSetupKeysConverted`
  (byte-preserving `configedit.PlanEdit`), `Target.Rewrote`, `setupLeft > 0` → `InequalityError`,
  the symlink rule at `graph.go:556`, `withRenamedPackRequests` for a project-scope file,
  `projectBrokerDeclaration`, `addWorkspaceBrokerGrants`, `workspaceGrantNextSteps`.
- Refusal: the removed-setup-key shape. `firstReadableTOMLViolation`
  (`readablevalidate.go:85`) lets the file load, so `yolo config migrate` and the edit verbs
  still reach it, and a semantic validator refuses it (`validateSetupsRetired`,
  `setupsretired.go:142`). The old `repos` key follows it: its schema rows stay, the resolver
  refuses.
- Broker fixtures, `internal/ghbroker`: `windowFixture`, `pending`, `runHeadJSON`,
  `afterGrantLook`, `serveGH`; `brokerscope.ReplaceScopeContext(ctx, b.authority.path,
  b.authority.scope, repos, grants)` as `workingset_test.go:88` calls it.
- Launch fixtures, `internal/cli/run/brokeredscope_requests_test.go:33-94`: `brokeredFixtureBlock`,
  `checkConfigChanges`, `startLoopholes`, `readHandedScope`. `liveapply_test.go:912`
  (`TestConfigApplyReplacesGrants`) for the apply seam.
- cli fixtures: `newNamedVerbFixture`, `recordAllowGate`, `captureStd`, `readFile`,
  `assertPublicPostWriteCheck`, `newMigrateEnv`, `writeMigrateFile`, `runConfigMigrateVerb`,
  `present`. configmigrate fixtures: `env`, `realDir`, `write`, `plan`, `target`, `targetText`,
  `treeState`.

## Traps

- **Two callers.** `ProduceReadableGateInput` and `WorkspaceBrokerGrantsOf` both fan
  `readableRequestGrants` over `targets`. Narrowing one makes the gate and the migration proof
  disagree with no test failing; `TestTheWorkspaceGrantProjectionAgreesWithTheGate` is the pin.
- **Pick origin by the remote, not by the label.** `appendReadableRepository` de-duplicates by
  repository and the first `Origin` label survives. Resolve the remote named `origin` from
  `remotes.Remotes` and match its identity.
- **Read scope must not shrink.** Every remote and every listed repository keeps its `read` grant
  and its row. Only the non-read request loop narrows. The `allow.<source>.always` loop
  (`readableallow.go:705-716`) is the user's ceiling acting for every workspace; leave it.
- **The ceiling is decided before remotes are read.** `ResolveReadableAllowance` skips a request
  outside `allow.<source>` at `:584-587` with no repository in hand. With `runs` only under a
  user `repos` table, the unqualified request dies there and never reaches the gate.
- **The broker has its own ceiling read.** `ghbroker/workingset.go:114` requires the kind in
  `BrokeredGrantKindsUsingReadablePolicy(Source)`, which reads `allow.<source>` only. Without
  Task 3's change a scoped ceiling lowers a grant the broker then answers "asks" for, and deleting
  the scoped table would not withdraw it live (BB-D96).
- **Path-string consumers.** A request at `repos.<name>.requests.<source>.<capability>` is dropped
  or misread by `filterReadablePlanToActiveBrokers` (`readablescope.go:223`), the three request
  loops in `ProduceReadableGateInput` (`:654`, `:671`, `:718`), `readableReadRequestedFromResults`
  (`:1621`), `readableGrantProvenance` (`readablescope.go:540`), `scopeCheckFromReadable`
  (`:661`, `:664`, `:687`, `:733`) and `brokeredSkippedRequests` (`brokeredskipped.go:56`).
- **Never read `repos` from the composed config.** `LoadReadableConfig` merges the user's
  `repos.mine` and a project's `repos.mine` into one table. Read it from `input.User` or
  `input.Workspace`, which the gate already holds apart (`readablescope.go:110-114`).
- **`gitRemotes` names remotes `ra`, `rb`** (`configmigrate_test.go:44`). After Task 1 an
  unqualified named-host line with no `origin` is unplaced, so four migration tests report
  inequality.
- **The proof goes vacuous if both sides read the new meaning.** `readCanonical`
  (`plan.go:~990`) hands the raw workspace map to `WorkspaceBrokerGrantsOf` for both sides. Once
  that function narrows, both sides agree while the migration drops every retained write. The
  before side reads the old-reach projection instead; the normal projection is never left old to
  make the proof pass.
- **The old reach applies only while an old spelling is present.** If the before side used it
  for a file already converted, a second `yolo config migrate` would write the remote tables
  again.
- **A pattern in a grant's `Repo` is inert today, read included.** Every grant match in the broker is
  `sameRepo`, an exact compare (`ghbroker/scope.go:56`); only scope membership uses
  `brokerscope.Admits` (`scope.go:108`). On the readable path a legacy pattern entry already
  lowers `read github.com/o/*` and fanned writes that no command matches, and the readable path
  refuses that read (exit 64). Probed at `35224f8b0` through a `go test -overlay`, not kept in the
  tree. Task 3 admits a pattern only on a grant marked `RepoPattern`, which only a named table's
  wildcard lowering sets; the matcher never keys on a `*` in `Repo`. `sameRepo` itself stays:
  `access.go:224` compares a run's head repository with it, where a pattern must never match.
- **Lines are copied from the merged workspace into the file that held the entry.** This
  repository's shape: the project file asks, the local file lists. Copying same-file lines only
  converts that shape to read-only, silently.
- **`tomlCeiling` / `withCeilingLines` writes the user config.** Not for this migration: it writes
  no user file, copies no `allow` word into a project, and no project line into a user table.
- **The retained-write print is identity, file and key.** Never a diff or a table: a project file
  can hold `env.secrets.set`.
- **`yolo allow` in a jail** hands the command to the human (`hostApprovalStep`); the new words
  reach that branch before any pack read, echoing the original argv.
- **cli tests run inside this jail.** Each goes through `newNamedVerbFixture` or `newMigrateEnv`,
  which clear `YOLO_VERSION` and `YOLO_WORKSPACE` and chdir. A test that skips them edits
  `/workspace`'s real config.
- **A launch-level test that reads a pack file** adds an outside-package read and a census
  change. Task 4's manifest block is a literal in the test.

## Settled for the whole series, do not re-open

Quoted from the design; the engineering bounds are [GW-D80](github-write-scopes.md#GW-D80).

1. "Each unqualified `[requests.github]` line, list or boolean, lowers to grants on one concrete
   identity, the `<host>/<path>` that `origin` resolves to, and on no other target."
2. **Which source narrows.** A `[brokered]` declaration opts in with
   `unqualified_requests = "origin"`. The shipped `github` broker declares it; core names no pack,
   source or tool. A selected named-host source that does not declare it, a third-party one on
   `github.com` included, keeps today's fan-out for its unqualified lines. The key is refused on
   an any-host source. `repos.<name>.requests.<broker>` and `.allow.<broker>` are generic over
   every selected named-host source.
3. One shared `[repos.<name>]` table. `repo` is `<host>/<path>`; `repo = "acme/infra"` is refused
   naming `github.com/acme/infra`. No `repo` line means the clone's remote of that name; no such
   remote is an error naming the remotes and `yolo request repos <name> <host>/<path>`.
4. Naming is asking to read, for each broker serving the host; git gets RC-D26's two URLs and no
   push. Writes come only from `[repos.<name>.requests.<broker>]`.
5. `[repos.origin.requests.github]` is refused naming `[requests.github]`. `all` is refused as a
   name. An unknown key under any `repos.<name>` is refused. `requests` under a name is
   workspace-only, `allow` user-only.
6. `repo` may put the whole-segment wildcard in the owner and repository segments, never the
   host, in a workspace and in the user config ([§8](github-write-scopes.md#8-which-repository-a-line-is-for)). A workspace pattern table lowers one
   grant per line with the pattern as its repository, marked as a pattern (`RepoPattern`), never enumerated and never `AnyRepo`; the
   scope row and each such grant line say in plain words what the pattern admits
   (`brokerscope.WildcardNote`); the broker matches a marked grant with `brokerscope.Admits`, behind the
   scope and read floor, and an unmarked grant only exactly. A pattern target is not handed to an any-host source: RC-D26 spells
   exact URLs. A pattern grant is inside a scoped ceiling when that user table's pattern covers
   the workspace's segment by segment. A user `repo` is required.
7. A declared name equal to a remote's name pins it: the table's lines and its read go to the
   declared repository; the remote stays in read scope as a remote; unqualified lines stay the
   remote's.
8. A user table's `always` and `on_request` add to `[allow.<broker>]` for the repositories its
   `repo` admits and never narrow it. Names meet only at the repository. The read capability
   under a name is refused naming `[allow.<broker>] always`.
9. Git lines under a name (`[repos.<name>.requests] push = …`) are not in this slice: refused,
   naming `yolo request git <capability>`.
10. A scalar `repo` that retargets a name, or an `origin` that resolves elsewhere, is a new
    identity: the gate shows old and new and asks again.
11. The migration preserves the whole set of write grants the old spelling reached: on each
    listed repository **and on each remote other than `origin`**, as explicit printed lines. A
    legacy pattern `brokered.<source>.repos` entry and a raw grant whose `repo` is a pattern are **not converted**, as a `repo = "*"` grant is not: an older gate recorded them on a pattern that matched no command, so no table equals them, and their read stays refused as today.
    A legacy grant with `repo = "*"` is **not converted**: it covers repositories nobody has
    declared, so no table equals it; its old file is kept and still read, with today's reason
    and next step. A TOML workspace that holds no old spelling is not rewritten: its other
    remotes lose the unqualified lines by [OQ-GW13](github-write-scopes.md#OQ-GW13), shown at
    the gate as removed, and `yolo request repos.<remote>.github …` asks for them back.
12. A config edit revokes nothing in a running jail. It takes effect at a fresh launch's gate or
    at host `yolo config apply`. No message says otherwise. Stored broker records are never
    rewritten, expired or revoked by a launch or a migration; `yolo approve --revoke` is the way.

## Tasks

Every task: `git status --short` empty first; `go env GOCACHE` is the shared cache; **do not set
`HOME`, `TMPDIR` or `GOCACHE`** for a build or a test run. `just format` before each commit.
Fences are tier 3: a request, nothing enforces them. Report for every task: the red output, the
green gates, and every other test that failed with its old assertion quoted.

### Task 1: origin-only lowering (unblocks the rest)

- **Settled.** Items 1, 2. "An unqualified GitHub line where `origin` is absent or not on
  `github.com`: error, not a skip. It names the next step … An unqualified line never falls back
  to another repository."
- **Tests first.** New `internal/config/reposcope_origin_test.go`, package `config`; imports
  `os`, `path/filepath`, `sort`, `strings`, `testing`, `brokerscope`. The file does not compile
  until `loopholedecl.Brokered` has the opt-in field: add the field and its decode first, with no
  reader, then confirm the reds. First the test observed red at `45f576cb4` (there with the
  broker built inline):

```go
func TestUnqualifiedGitHubRequestsLowerToOriginOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeReadableScopeUser(t, home, "[allow.github]\nalways = [\"read\"]\non_request = [\"runs\", \"approve_runs\"]\n")
	workspace := t.TempDir()
	writeReadableScopeRemote(t, workspace, "[remote \"origin\"]\n\turl = https://github.com/acme/app.git\n[remote \"upstream\"]\n\turl = https://github.com/elsewhere/app.git\n")
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName), []byte("[requests.github]\nruns = [\"*\"]\napprove_runs = [\"github-actions[bot]\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	broker := repoScopeBroker()
	check := NewScopeCheck(workspace, []ScopeBroker{broker}, read)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	grants := check.Sources[0].Grants
	if !containsGrant(grants, brokerscope.Grant{Kind: brokerscope.KindRuns, Repo: "github.com/acme/app", Workflow: "*"}) {
		t.Fatalf("origin lost its runs grant: %v", grants)
	}
	if !containsGrant(grants, brokerscope.Grant{Kind: brokerscope.KindRead, Repo: "github.com/elsewhere/app"}) {
		t.Fatalf("the second remote left the read scope: %v", grants)
	}
	for _, g := range grants {
		if g.Kind != brokerscope.KindRead && g.Repo != "github.com/acme/app" {
			t.Errorf("an unqualified request reached a repository other than origin: %s", g.String())
		}
	}
}
```

  Then the helpers and two more tests, same file:

```go
func repoScopeBroker() ScopeBroker {
	kinds := map[string]string{"runs": "runs", "approve-run": "approve_runs"}
	broker := readableScopeBroker("github", "github", "github-broker", "github.com", "read", []string{"runs", "approve-run"}, kinds)
	// What packs/github declares: its unqualified requests are for origin.
	broker.Readable.Brokered.UnqualifiedRequests = "origin"
	return broker
}

// repoScopeWorkspace writes the user config, the clone's git config and the two workspace
// files ("" leaves one out), and returns the workspace.
func repoScopeWorkspace(t *testing.T, user, gitConfig, project, local string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeReadableScopeUser(t, home, user)
	workspace := t.TempDir()
	if gitConfig != "" {
		writeReadableScopeRemote(t, workspace, gitConfig)
	}
	for name, body := range map[string]string{WorkspaceTOMLConfigName: project, WorkspaceLocalTOMLConfigName: local} {
		if body == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return workspace
}

func repoScopeCheck(t *testing.T, workspace string) (*ScopeCheck, *WorkspaceRead) {
	t.Helper()
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	return NewScopeCheck(workspace, []ScopeBroker{repoScopeBroker()}, read), read
}

func repoScopeWrites(grants []brokerscope.Grant) []string {
	var out []string
	for _, g := range grants {
		if g.Kind != brokerscope.KindRead {
			out = append(out, g.String())
		}
	}
	sort.Strings(out)
	return out
}

const repoScopeUserAll = "[allow.github]\nalways = [\"read\"]\non_request = [\"runs\", \"approve_runs\"]\n"
const repoScopeOriginLines = "[requests.github]\nruns = [\"*\"]\napprove_runs = [\"github-actions[bot]\"]\n"

func TestUnqualifiedGitHubRequestsWithoutAnOriginAreAnError(t *testing.T) {
	for _, c := range []struct{ name, gitConfig string }{
		{"the only remote is upstream", "[remote \"upstream\"]\n\turl = https://github.com/elsewhere/app.git\n"},
		{"origin is on another host", "[remote \"origin\"]\n\turl = https://gitlab.com/acme/app.git\n[remote \"upstream\"]\n\turl = https://github.com/elsewhere/app.git\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			workspace := repoScopeWorkspace(t, repoScopeUserAll, c.gitConfig, repoScopeOriginLines, "")
			check, _ := repoScopeCheck(t, workspace)
			if check.Fatal == nil {
				t.Fatalf("no error: an unqualified line fell back to another repository: %#v", check.Sources)
			}
			for _, want := range []string{"origin", "[repos.<name>]"} {
				if !strings.Contains(check.Fatal.Error(), want) {
					t.Errorf("the error does not name %q: %v", want, check.Fatal)
				}
			}
			for _, s := range check.Sources {
				if w := repoScopeWrites(s.Grants); len(w) != 0 {
					t.Errorf("a refused scope still carries %v", w)
				}
			}
		})
	}
}

// The gate and the migration proof's projection lower one workspace's ask to the same grants.
func TestTheWorkspaceGrantProjectionAgreesWithTheGate(t *testing.T) {
	workspace := repoScopeWorkspace(t, repoScopeUserAll,
		"[remote \"origin\"]\n\turl = https://github.com/acme/app.git\n[remote \"upstream\"]\n\turl = https://github.com/elsewhere/app.git\n",
		repoScopeOriginLines, "")
	check, read := repoScopeCheck(t, workspace)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	var projected []string
	for _, w := range WorkspaceBrokerGrantsOf(read.Config, workspace, []ReadableBrokerDeclaration{*repoScopeBroker().Readable}) {
		for _, g := range w.Grants {
			projected = append(projected, g.String())
		}
	}
	sort.Strings(projected)
	if got := repoScopeWrites(check.Sources[0].Grants); strings.Join(got, "\n") != strings.Join(projected, "\n") {
		t.Fatalf("the gate lowers\n%v\nand the workspace projection\n%v", got, projected)
	}
}
```

```go
const repoScopeTwoRemotes = "[remote \"origin\"]\n\turl = https://github.com/acme/app.git\n[remote \"upstream\"]\n\turl = https://github.com/elsewhere/app.git\n"

// A named-host source whose declaration does not opt in keeps today's reach on every remote.
func TestANamedHostSourceThatDoesNotOptInKeepsItsFanOut(t *testing.T) {
	workspace := repoScopeWorkspace(t, repoScopeUserAll, repoScopeTwoRemotes, repoScopeOriginLines, "")
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	broker := repoScopeBroker()
	broker.Readable.Brokered.UnqualifiedRequests = ""
	check := NewScopeCheck(workspace, []ScopeBroker{broker}, read)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	want := []string{"approve-run github.com/acme/app github-actions[bot]", "approve-run github.com/elsewhere/app github-actions[bot]",
		"runs github.com/acme/app *", "runs github.com/elsewhere/app *"}
	if got := repoScopeWrites(check.Sources[0].Grants); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("a source that did not opt in lowers %v, want %v", got, want)
	}
}

// The old projection keeps today's fan-out for the migration proof's before side; the new one
// is the gate's own lowering. Neither is the other.
func TestTheLegacyBaselineFansOutAndTheNewProjectionDoesNot(t *testing.T) {
	gitConfig := "[remote \"origin\"]\n\turl = https://github.com/acme/app.git\n[remote \"upstream\"]\n\turl = https://github.com/elsewhere/app.git\n"
	project := "[requests.github]\nrepos = [\"acme/extra\"]\nruns = [\"ci.yml\"]\n"
	workspace := repoScopeWorkspace(t, repoScopeUserAll, gitConfig, project, "")
	check, read := repoScopeCheck(t, workspace)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	declarations := []ReadableBrokerDeclaration{*repoScopeBroker().Readable}
	strs := func(all []WorkspaceBrokerGrants) []string {
		var out []string
		for _, w := range all {
			for _, g := range w.Grants {
				out = append(out, g.String())
			}
		}
		sort.Strings(out)
		return out
	}
	before := strs(LegacyWorkspaceBrokerGrantsOf(read.Config, workspace, declarations))
	wantBefore := []string{"runs github.com/acme/app ci.yml", "runs github.com/acme/extra ci.yml", "runs github.com/elsewhere/app ci.yml"}
	if strings.Join(before, "\n") != strings.Join(wantBefore, "\n") {
		t.Fatalf("the legacy baseline lowers %v, want today's fan-out %v", before, wantBefore)
	}
	after := strs(WorkspaceBrokerGrantsOf(read.Config, workspace, declarations))
	if len(after) != 1 || after[0] != "runs github.com/acme/app ci.yml" {
		t.Fatalf("the new projection lowers %v, want origin's line only", after)
	}
	if got := repoScopeWrites(check.Sources[0].Grants); strings.Join(got, "\n") != strings.Join(after, "\n") {
		t.Fatalf("the gate lowers %v and the new projection %v", got, after)
	}
}
```

  Red: `go test -count=1 -run 'TestUnqualifiedGitHubRequests' ./internal/config`. Observed for
  the first: `an unqualified request reached a repository other than origin: runs github.com/elsewhere/app *`
  and the same for `approve-run github.com/elsewhere/app github-actions[bot]`. Predicted for
  `…WithoutAnOriginAreAnError`, both cases: `no error: an unqualified line fell back to another repository`.
  `TestTheWorkspaceGrantProjectionAgreesWithTheGate` and
  `TestANamedHostSourceThatDoesNotOptInKeepsItsFanOut` are **pins**, green before and after.
  `TestTheLegacyBaselineFansOutAndTheNewProjectionDoesNot` does not compile until the baseline
  exists; with it and no narrowing, it fails at `the new projection lowers …`.
- **Change.**
  - `loopholedecl.Brokered` gains the opt-in (decode beside `pending_capabilities`; validation
    refuses any value but `origin`, and the key on `AnyRemoteHost`). `packs/github`'s manifest
    sets it. `ReadableBrokerPolicy` carries it from the declaration.
  - One origin lookup, used by both callers for an opted-in source. In
    `ProduceReadableGateInput` the error fires only when the source has an unqualified request
    that would lower a grant (granted, not read, not pending, mapped to a kind); it is checked
    after the "read not allowed" skip, replaces the "no concrete repository target" skip
    (`:668-680`) for that source, and surfaces through `readableScopeFailure`.
    `WorkspaceBrokerGrantsOf` narrows identically, listed entries included; it returns no error,
    so a missing origin goes in `Unplaced`.
  - The lookup is `readableOriginTarget(policy ReadableBrokerPolicy, remotes brokerscope.Read) (ReadableRepository, error)`.
  - `LegacyWorkspaceBrokerGrantsOf`, same signature as `WorkspaceBrokerGrantsOf`: today's body
    moved verbatim, fanning every unqualified non-read line over every remote on the host and
    every listed entry, whatever the declaration opts into. Its doc comment says it is a
    migration baseline and grants nothing. It knows no `repos` root and reports one as an
    `Unplaced` reason, so it cannot serve as an after side.
  - `configmigrate/brokered.go:86`, `addWorkspaceBrokerGrants`: the **before** side calls the
    legacy baseline when the migration writes a workspace file, and the normal projection when
    it writes none (a user-only conversion, or a second run, compares like with like). The
    **after** side always calls the normal projection. `readCanonical` (`plan.go:~990`) passes
    which.
- **Rewrites, not repairs.**

| Test | Old assertion | New |
| :--- | :--- | :--- |
| `TestReadableDispatchWorkflowsRequestBecomesAGrantPerTarget`, `readabledispatch_test.go:23` | `runs github.com/acme/private nightly-macos.yml` and `… *` from `repos = ["acme/private"]`, no remote | an `origin` fixture; the grants on origin, none on the listed repository |
| `TestReadableApproveRunsRequestBecomesAGrantPerTarget`, `readableapproverun_test.go:25` | `approve-run github.com/acme/private github-actions[bot]` and `… *` | the same shape |
| `TestPlanConvertsADispatchWorkflowGrant` (`configmigrate_test.go:150`), `TestPlanConvertsAnApproveRunGrant` (`:172`), the github kinds of `TestEveryMappedGrantKindOfEveryShippedBrokerConverts` (`projectgrants_test.go:84`), `TestTheProofNamesAWorkspaceGrantTheConversionChanges` (`workspacegrants_test.go:38`) | remote `ra` | the fixture's sole remote is named `origin`; assertions unchanged. `lower.go` is not edited: a sole remote not named `origin` now reports inequality and writes nothing |

- **Gates.** `go test -count=1 ./internal/config ./internal/cli/... ./internal/configmigrate ./internal/ghbroker`.
- **Fences.** `internal/config`; `internal/loopholedecl` (the one field); the one manifest line;
  `internal/configmigrate/brokered.go`, `plan.go` (the one call) and
  `*_test.go` fixtures. Not `lower.go`, `internal/ghbroker`, a schema
  row or a verb. `internal/loopholedecl/testdata/legacy-shipped/*` is not edited.
- **Commit.** `feat(config): a broker may declare its unqualified requests are for origin; github does (GW-D79)`.

### Task 2: `[repos.<name>]` tables (after 1)

- **Settled.** Items 3 to 7, 10.
- **Tests first.** New `internal/config/reposcope_tables_test.go`, package `config`, Task 1's
  imports and helpers:

```go
const repoScopeOrigin = "[remote \"origin\"]\n\turl = https://github.com/acme/app.git\n"

func TestAReposTableIsReadAndAnUnknownKeyUnderItIsRefused(t *testing.T) {
	workspace := repoScopeWorkspace(t, repoScopeUserAll, repoScopeOrigin, "[repos.infra]\nrepo = \"github.com/acme/infra\"\n", "")
	if _, err := ReadWorkspaceForGate(workspace); err != nil {
		t.Fatalf("a [repos.<name>] table with a repo line is refused: %v", err)
	}
	workspace = repoScopeWorkspace(t, repoScopeUserAll, repoScopeOrigin, "[repos.infra]\nrepo = \"github.com/acme/infra\"\nrepoz = \"x\"\n", "")
	if _, err := ReadWorkspaceForGate(workspace); err == nil || !strings.Contains(err.Error(), "repos.infra.repoz") {
		t.Fatalf("an unknown key under a repos table: %v", err)
	}
}

func TestANamedRepositoryInALocalFileIsReadOnly(t *testing.T) {
	workspace := repoScopeWorkspace(t, repoScopeUserAll, repoScopeOrigin, repoScopeOriginLines,
		"[repos.notes]\nrepo = \"github.com/example/notes\"\n")
	check, _ := repoScopeCheck(t, workspace)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	s := check.Sources[0]
	if !containsGrant(s.Grants, brokerscope.Grant{Kind: brokerscope.KindRead, Repo: "github.com/example/notes"}) {
		t.Fatalf("the named repository is not in the read scope: %v", s.Grants)
	}
	if !readableScopeEntry(s.Entry, "example/notes", WorkspaceLocalTOMLConfigName) {
		t.Fatalf("the scope row does not name the local file: %#v", s.Entry)
	}
	want := []string{"approve-run github.com/acme/app github-actions[bot]", "runs github.com/acme/app *"}
	if got := repoScopeWrites(s.Grants); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("writes are %v, want origin's own and none on the named repository: %v", got, want)
	}
}

func TestANamedTablesRequestLowersToItsOwnRepositoryOnly(t *testing.T) {
	project := "[repos.tools]\nrepo = \"github.com/example/tools\"\n[repos.tools.requests.github]\nruns = [\"lint.yml\"]\n"
	workspace := repoScopeWorkspace(t, repoScopeUserAll, repoScopeOrigin, project, "")
	check, read := repoScopeCheck(t, workspace)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	s := check.Sources[0]
	grant := brokerscope.Grant{Kind: brokerscope.KindRuns, Repo: "github.com/example/tools", Workflow: "lint.yml"}
	if got := repoScopeWrites(s.Grants); len(got) != 1 || got[0] != grant.String() {
		t.Fatalf("writes are %v, want only %s", got, grant.String())
	}
	if origin := s.GrantOrigins[grant.String()]; !strings.Contains(origin, WorkspaceTOMLConfigName) || !strings.Contains(origin, "repos.tools.requests.github.runs") {
		t.Fatalf("the grant's gate line ends with %q, want the file and repos.tools.requests.github.runs", origin)
	}
	var projected []string
	for _, w := range WorkspaceBrokerGrantsOf(read.Config, workspace, []ReadableBrokerDeclaration{*repoScopeBroker().Readable}) {
		for _, g := range w.Grants {
			projected = append(projected, g.String())
		}
	}
	if len(projected) != 1 || projected[0] != grant.String() {
		t.Fatalf("the workspace projection lowers %v, want only %s", projected, grant.String())
	}
}

func TestATableWithNoRepoLineMeansTheRemoteOfThatName(t *testing.T) {
	gitConfig := repoScopeOrigin + "[remote \"upstream\"]\n\turl = https://github.com/elsewhere/app.git\n"
	workspace := repoScopeWorkspace(t, repoScopeUserAll, gitConfig, "[repos.upstream.requests.github]\nruns = [\"ci.yml\"]\n", "")
	check, _ := repoScopeCheck(t, workspace)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	if got := repoScopeWrites(check.Sources[0].Grants); len(got) != 1 || got[0] != "runs github.com/elsewhere/app ci.yml" {
		t.Fatalf("writes are %v, want the upstream remote's repository only", got)
	}
}

func TestARetargetedNameIsANewIdentityAtTheGate(t *testing.T) {
	project := "[repos.tools]\nrepo = \"github.com/example/tools\"\n[repos.tools.requests.github]\nruns = [\"lint.yml\"]\n"
	workspace := repoScopeWorkspace(t, repoScopeUserAll, repoScopeOrigin, project, "")
	check, _ := repoScopeCheck(t, workspace)
	if err := RecordApproval(workspace, decode(t, `{}`), check); err != nil {
		t.Fatal(err)
	}
	local := "[repos.tools]\nrepo = \"github.com/example/tools-fork\"\n"
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceLocalTOMLConfigName), []byte(local), 0o600); err != nil {
		t.Fatal(err)
	}
	check, _ = repoScopeCheck(t, workspace)
	report, err := ScopeChanges(workspace, check)
	if err != nil {
		t.Fatal(err)
	}
	block := strings.Join(report.ScopeBlock, "\n")
	if !report.ScopeChanged {
		t.Fatalf("a retargeted name did not ask again:\n%s", block)
	}
	for _, want := range []string{"  - example/tools ", "  + example/tools-fork ",
		"  - grant runs github.com/example/tools lint.yml", "  + grant runs github.com/example/tools-fork lint.yml"} {
		if !strings.Contains(block, want) {
			t.Errorf("the gate does not show %q:\n%s", want, block)
		}
	}
}

func TestAReposTableThatCannotBeResolvedIsAnErrorWithItsNextStep(t *testing.T) {
	for _, c := range []struct {
		name, project string
		want          []string
	}{
		{"no repo line and no remote of that name", "[repos.nowhere.requests.github]\nruns = [\"ci.yml\"]\n",
			[]string{"repos.nowhere", "origin", "yolo request repos nowhere"}},
		{"a repo with no host", "[repos.infra]\nrepo = \"acme/infra\"\n", []string{"repos.infra.repo", "github.com/acme/infra"}},
		{"origin's GitHub lines have one home", "[repos.origin.requests.github]\nruns = [\"ci.yml\"]\n",
			[]string{"repos.origin.requests.github", "[requests.github]"}},
		{"all is not a name", "[repos.all]\nrepo = \"github.com/acme/infra\"\n", []string{"repos.all"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			workspace := repoScopeWorkspace(t, repoScopeUserAll, repoScopeOrigin, c.project, "")
			read, err := ReadWorkspaceForGate(workspace)
			text := ""
			if err != nil {
				text = err.Error()
			} else if check := NewScopeCheck(workspace, []ScopeBroker{repoScopeBroker()}, read); check.Fatal != nil {
				text = check.Fatal.Error()
			}
			if text == "" {
				t.Fatal("no error")
			}
			for _, want := range c.want {
				if !strings.Contains(text, want) {
					t.Errorf("the error does not name %q: %s", want, text)
				}
			}
		})
	}
}

func TestRequestsAndAllowUnderANameAreEachRefusedInTheOthersFile(t *testing.T) {
	workspace := repoScopeWorkspace(t, repoScopeUserAll, repoScopeOrigin,
		"[repos.infra]\nrepo = \"github.com/acme/infra\"\n[repos.infra.allow.github]\non_request = [\"runs\"]\n", "")
	if _, err := ReadWorkspaceForGate(workspace); err == nil || !strings.Contains(err.Error(), "repos.infra.allow") {
		t.Fatalf("a project file's ceiling under a name: %v", err)
	}
	user := repoScopeUserAll + "[repos.mine]\nrepo = \"github.com/acme/*\"\n[repos.mine.requests.github]\nruns = [\"ci.yml\"]\n"
	workspace = repoScopeWorkspace(t, user, repoScopeOrigin, repoScopeOriginLines, "")
	check, _ := repoScopeCheck(t, workspace)
	if check.Fatal == nil || !strings.Contains(check.Fatal.Error(), "repos.mine.requests") {
		t.Fatalf("a user config's request under a name: fatal %v", check.Fatal)
	}
}
```

  Same file:

```go
func TestAWildcardTableLowersOnePatternGrantPerLine(t *testing.T) {
	project := "[repos.fleet]\nrepo = \"github.com/acme/*\"\n[repos.fleet.requests.github]\nruns = [\"lint.yml\"]\n"
	workspace := repoScopeWorkspace(t, repoScopeUserAll, repoScopeOrigin, project, "")
	check, _ := repoScopeCheck(t, workspace)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	s := check.Sources[0]
	if got := repoScopeWrites(s.Grants); len(got) != 1 || got[0] != "runs pattern:github.com/acme/* lint.yml" {
		t.Fatalf("writes are %v, want one pattern grant and nothing enumerated or on every repository", got)
	}
	if !readableScopeEntry(s.Entry, "acme/*", WorkspaceTOMLConfigName) {
		t.Fatalf("the pattern is not a scope row: %#v", s.Entry)
	}
	report, err := ScopeChanges(workspace, check)
	if err != nil {
		t.Fatal(err)
	}
	block := strings.Join(report.ScopeBlock, "\n")
	for _, line := range strings.Split(block, "\n") {
		if strings.Contains(line, "grant runs pattern:github.com/acme/* lint.yml") && !strings.Contains(line, "every repository under acme") {
			t.Errorf("the pattern grant's line does not say what it admits: %s", line)
		}
	}
	if !strings.Contains(block, "grant runs pattern:github.com/acme/* lint.yml") {
		t.Fatalf("the gate shows no pattern grant:\n%s", block)
	}
}
```

A second test in the same file. A pattern write an older gate recorded is not a named table's
approval, so the gate asks for the table once (GW-D80). Proposed, not run: it uses the helpers
the probe used, which Task 2 confirms or adds.

```go
// A pattern write an older gate recorded matched no command. A named pattern table that lowers the
// same repository and workflow is a new grant: the gate shows it added, with its plain words, and
// asks, and the record never stands in for that approval (GW-D80).
func TestANamedPatternTableIsAskedForOverARecordedLegacyPatternGrant(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeReadableScopeUser(t, home, "[allow.github]\nalways = [\"read\"]\non_request = [\"runs\"]\n")
	workspace := t.TempDir()
	writeReadableScopeRemote(t, workspace, "[remote \"origin\"]\n\turl = https://github.com/acme/app.git\n")
	broker := readableScopeBroker("github", "github", "github-broker", "github.com", "read", []string{"runs"}, map[string]string{"runs": "runs"})
	check := func() *ScopeCheck {
		t.Helper()
		read, err := ReadWorkspaceForGate(workspace)
		if err != nil {
			t.Fatal(err)
		}
		c := NewScopeCheck(workspace, []ScopeBroker{broker}, read)
		if c.Fatal != nil || len(c.Sources) != 1 {
			t.Fatalf("scope check: fatal %v, sources %#v", c.Fatal, c.Sources)
		}
		return c
	}
	legacy := brokerscope.Grant{Kind: brokerscope.KindRuns, Repo: "github.com/acme/*", Workflow: "ci.yml"}

	// The old spelling, approved: the entry and a grant on it, as an older gate recorded them.
	old := filepath.Join(workspace, WorkspaceConfigName)
	if err := os.WriteFile(old, []byte(`{"packs":["github"],"brokered":{"github":{"repos":["acme/*"],"grants":[{"kind":"runs","repo":"github.com/acme/*","workflow":"ci.yml"}]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before := check()
	if !containsGrant(before.Sources[0].Grants, legacy) {
		t.Fatalf("the legacy entry no longer lowers its inert grant: %v", before.Sources[0].Grants)
	}
	if err := RecordApproval(workspace, nil, before); err != nil {
		t.Fatal(err)
	}
	if got := grantStrings(ApprovedGrants(workspace, "github")); !inList(got, "runs github.com/acme/* ci.yml") {
		t.Fatalf("the record holds %v", got)
	}
	if rep, err := ScopeChanges(workspace, check()); err != nil || rep.ScopeChanged {
		t.Fatalf("the approved legacy scope asks again: %v\n%s", err, strings.Join(rep.ScopeBlock, "\n"))
	}

	// The same reach, written as a named pattern table.
	if err := os.Remove(old); err != nil {
		t.Fatal(err)
	}
	table := "[repos.fleet]\nrepo = \"github.com/acme/*\"\n\n[repos.fleet.requests.github]\nruns = [\"ci.yml\"]\n"
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName), []byte(table), 0o600); err != nil {
		t.Fatal(err)
	}
	after := check()
	approved := legacy
	approved.RepoPattern = true
	if !containsGrant(after.Sources[0].Grants, approved) || containsGrant(after.Sources[0].Grants, legacy) {
		t.Fatalf("the table lowered %v, want the grant marked as a pattern and no unmarked one", after.Sources[0].Grants)
	}
	rep, err := ScopeChanges(workspace, after)
	if err != nil || !rep.ScopeChanged {
		t.Fatalf("a recorded legacy grant stood in for the table's approval: changed %v, %v", rep.ScopeChanged, err)
	}
	added := false
	for _, line := range rep.ScopeBlock {
		if strings.Contains(line, "grant runs pattern:github.com/acme/* ci.yml") {
			added = strings.Contains(line, "added") && strings.Contains(line, "every repository under acme")
		}
	}
	if !added {
		t.Fatalf("the gate does not show the pattern grant added with what it admits:\n%s", strings.Join(rep.ScopeBlock, "\n"))
	}
	if err := RecordApproval(workspace, nil, after); err != nil {
		t.Fatal(err)
	}
	if rep, err := ScopeChanges(workspace, check()); err != nil || rep.ScopeChanged {
		t.Fatalf("the approved table asks again: %v\n%s", err, strings.Join(rep.ScopeBlock, "\n"))
	}
}
```

```go
// A workspace whose only readable key is a repos table still takes the readable path, under a
// user config that grants read in the old spelling.
func TestAReposTableAloneSelectsTheReadableScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	userDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := `{"brokered": {"github": {"grants": [{"kind": "read", "repo": "*"}]}}}`
	if err := os.WriteFile(filepath.Join(userDir, paths.UserConfigLegacyJSONCName), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	writeReadableScopeRemote(t, workspace, repoScopeOrigin)
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName), []byte("[repos.notes]\nrepo = \"github.com/example/notes\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	check, _ := repoScopeCheck(t, workspace)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	if check.ReadableInput == nil {
		t.Fatal("a repos table alone fell to the legacy scope producer")
	}
	if !readableScopeEntry(check.Sources[0].Entry, "example/notes", WorkspaceTOMLConfigName) {
		t.Fatalf("the named repository is not in the scope: %#v", check.Sources[0].Entry)
	}
}
```

  `TestAReposTableAloneSelectsTheReadableScope` adds an import of `internal/paths`. Two more
  rows for `TestAReposTableThatCannotBeResolvedIsAnErrorWithItsNextStep`:

```go
		{"a Git line under a name is not built yet", "[repos.infra]\nrepo = \"github.com/acme/infra\"\n[repos.infra.requests]\npush = [\"main\"]\n",
			[]string{"repos.infra.requests.push", "yolo request git push main"}},
		{"a table that says nothing", "[repos.upstream]\n", []string{"repos.upstream"}},
```

  For the first of those rows build the check with both brokers, and give the user config
  `[allow.git]\nalways = ["fetch"]\non_request = ["push"]\n` as well:

```go
	brokers := []ScopeBroker{repoScopeBroker(),
		readableScopeBroker("git", "git", "git-broker", "*", "fetch", []string{"branch"}, map[string]string{"branch": "push"})}
```

  New `internal/cli/run/brokeredscope_repos_test.go`: copy
  `TestAReadableRequestedRepoIsApprovedAtTheGateAndReachesTheBroker`'s "accepted" case
  (`brokeredscope_requests_test.go:33-94`) with its setup closure, as
  `TestANamedRepositoryIsApprovedAtTheGateAndReachesTheBroker`. One change: the workspace file is
  `"[repos.outside]\nrepo = \"github.com/" + repo + "\"\n"`. Keep its three assertions (the gate
  shows `acme/outside`; `config.ApprovedScope(o.Workspace, "github")` is `acme/outside`; the
  handed scope file's `Repos` is `acme/outside`). It fails if the launch's call site is deleted.

  Red: `go test -count=1 -run 'TestARepos|TestANamed|TestATable|TestARetargeted|TestRequestsAndAllow|TestAWildcardTable' ./internal/config ./internal/cli/run`.
  Predicted: each fails at the strict load, `repos` matching no schema row; the launch test
  prints `an accepted gate refused`. The project-file half of `TestRequestsAndAllow…` and the
  `all` case can be green early for the wrong reason; they prove nothing until the first subtest
  of `TestAReposTableIsReadAndAnUnknownKeyUnderItIsRefused` passes.
- **Change.**
  - Schema rows: `repos`, `repos.*` (both, object), `repos.*.repo` (both, string),
    `repos.*.requests` (workspace, object), `repos.*.requests.*` (workspace, `SchemaTypeAny`).
    The walk stops at an `any` row (`readablevalidate.go:116`), so the value reaches the
    resolver, the only place that knows the selected packs. No list row, so the element-row and
    removal-policy censuses need nothing. No `allow` row yet: a user file with a scoped ceiling
    is refused between Tasks 2 and 3.
  - Under `repos.<name>.requests` the resolver accepts a table keyed by the source of a selected
    `[brokered]` pack whose `remote_host` is the repository's host, its lines validated as the
    unqualified table's are. It refuses, each with its next step: a broker on another host,
    naming those that serve it; a call-broker source; a capability of a selected any-host source
    (read from that pack's `kind_capabilities`), per settled item 9; anything else, listing the
    declared brokers; a table with neither `repo` nor any line.
  - Censuses the root pulls in: a `readableInheritCensus` entry (`inherit.go:411`, neither flag
    set, as `allow` and `requests`); `[bold]repos[/bold]` in `config_ref.txt`'s TOML section;
    `withReadableScopeKeys` (`mountsrequest.go:116`).
  - `ResolveReadableAllowance`: read `repos` from `input.Workspace`; validate names, `repo`,
    the remote-name form, the wrong-scope subtable (a JSONC file has no schema), and each
    capability with the unqualified path's checks (`:536-575`). A broker under a name is a
    selected `[brokered]` source whose `remote_host` equals the repository's host; a call broker
    is refused.
  - `ReadableRequestResult` gains the source and the table name as fields; every path-string
    consumer in Traps reads them. `Path` stays the authored key, which the gate prints.
  - `scopeCheckFromReadable` gives a named target an `Entry` row with its declaring files. A
    named target on an any-host source follows the requested-only path (`:687-694`).
    `readableRequestsOutsideProblems` (`gateread.go:59`) also walks `repos`;
    `WorkspaceBrokerGrantsOf` projects named tables. The legacy paths (`collectLegacyReadableRepositories`,
    `applyLegacyReadableWorkspace`, `legacyWorkspaceGrant`) never mark a grant. No lowering emits
    `AnyRepo`, an unmarked pattern from a named table, or a marked grant from anything else.
  - A workspace pattern `repo` lowers per settled item 6, and every grant a named table's wildcard
    `repo` lowers, read included, is marked `RepoPattern` (new field in `brokerscope/grant.go`;
    `Grant.String` spells it `pattern:<repo>` through `PatternRepoPrefix`, which `ValidGrantRepo`
    refuses as a repository; `ParseGrant` and `ValidGrant` follow). `GrantNote`
    (`brokerscope/grant.go:267`) notes a wildcard name only; a marked grant also ends with
    `WildcardNote`'s words, and no unmarked grant gets them. It raises no "cannot be encoded … `git remote add`" note.
  - `identityRequired` (`readablescope.go:41`) is true when the user config has `allow` or
    `repos`, or the workspace has `requests` or `repos`.
  - `editvalidation_semantics.go:20-100`: add `repos` to its raw-scope roots, beside `requests`
    and `allow`, so an edit verb's semantic check sees the table.
  - The old `requests.<source>.repos` key keeps loading; its refusal is Task 7's.
- **Rewrites.** `internal/config/readablescope_test.go:149, 226, 253, 285, 439, 510, 634-656, 688-700`,
  `readableallow_test.go:132-133, 180-181, 387-388, 624, 635-636, 674`, `vacuousscope_test.go:129`:
  each that asserts a *write* on a listed repository is rewritten to the named-table form. One
  that asserts only read scope for the old key stays until Task 7.
- **Cheap.** Helper and field names; one validation loop or two; refusal wording beyond the
  strings the tests name and a next step; the census reason text. The scope row shows the
  declaring file; the table name is on the grant line through `GrantOrigins`. No new recorded
  source key.
- **Gates.** `go test -count=1 ./internal/config ./internal/cli/... ./internal/configmigrate ./internal/configedit ./internal/ghbroker ./internal/gitbroker ./internal/loopholes`.
- **Fences.** `internal/config`, `internal/brokerscope/grant.go` and its test (the field, the prefix,
  `String`, `ParseGrant`, `ValidGrant`, `GrantNote`), the new `internal/cli/run` test, one line in
  `internal/cli/config_ref.txt`. No verb, no refusal of the old key.
- **Commit.** `feat(config): [repos.<name>] tables, read-only unless their own table asks (GW-D76, GW-D79)`.

### Task 3: the scoped ceiling, at the gate and in the running broker (after 2)

- **Settled.** Items 6, 8. "A request above the ceiling is skipped and reported, as now."
- **Tests first.** New `internal/config/reposcope_ceiling_test.go`, package `config`:

```go
const repoScopeUserScoped = "[allow.github]\nalways = [\"read\"]\n" +
	"[repos.jail]\nrepo = \"github.com/acme/*\"\n[repos.jail.allow.github]\non_request = [\"runs\"]\n"

// The project's table is named jail too, and points somewhere the user's jail does not admit.
const repoScopeProjectTwoRepos = "[requests.github]\nruns = [\"*\"]\n" +
	"[repos.jail]\nrepo = \"github.com/example/tools\"\n[repos.jail.requests.github]\nruns = [\"lint.yml\"]\n"

func TestAScopedCeilingAdmitsARequestByRepositoryNeverByName(t *testing.T) {
	workspace := repoScopeWorkspace(t, repoScopeUserScoped, repoScopeOrigin, repoScopeProjectTwoRepos, "")
	check, _ := repoScopeCheck(t, workspace)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	s := check.Sources[0]
	if got := repoScopeWrites(s.Grants); len(got) != 1 || got[0] != "runs github.com/acme/app *" {
		t.Fatalf("writes are %v, want only origin's, which the user's pattern admits", got)
	}
	skipped := false
	for _, r := range check.ReadableInput.Requests {
		if r.Path == "repos.jail.requests.github.runs" {
			skipped = r.Status == ReadableRequestSkipped
		}
	}
	if !skipped {
		t.Fatalf("the request outside every ceiling was not reported as skipped: %#v", check.ReadableInput.Requests)
	}
	for _, e := range s.Entry {
		if e.Repo != "example/tools" {
			t.Errorf("a user config's name put %q in this workspace's scope", e.Repo)
		}
	}
}

func TestAScopedCeilingAddsToTheBroadOne(t *testing.T) {
	user := strings.Replace(repoScopeUserScoped, "always = [\"read\"]\n", "always = [\"read\"]\non_request = [\"runs\"]\n", 1)
	workspace := repoScopeWorkspace(t, user, repoScopeOrigin, repoScopeProjectTwoRepos, "")
	check, _ := repoScopeCheck(t, workspace)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	want := []string{"runs github.com/acme/app *", "runs github.com/example/tools lint.yml"}
	if got := repoScopeWrites(check.Sources[0].Grants); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("writes are %v, want %v: a repos table never narrows [allow.github]", got, want)
	}
}
```

  New `internal/ghbroker/repobinding_test.go`, package `ghbroker`; imports `bytes`, `context`,
  `os`, `path/filepath`, `strings`, `sync`, `testing`, `brokerscope`, `brokerstore`, `paths`.
  Fixtures first (Task 4 adds its tests to this file):

```go
package ghbroker

// twoRepoGH is a host gh answering the broker's own reads for any repository and recording
// every other command as a write.
type twoRepoGH struct {
	mu  sync.Mutex
	ran [][]string
}

func (f *twoRepoGH) execute(_ context.Context, argv []string, _ []byte, out, _ func([]byte)) RunResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(argv) == 2 && argv[0] == "api" && strings.HasPrefix(argv[1], "repos/") {
		parts := strings.Split(strings.TrimPrefix(argv[1], "repos/"), "/")
		repo := strings.Join(parts[:2], "/")
		switch {
		case len(parts) == 2:
			out([]byte(`{"full_name":"` + repo + `","default_branch":"main"}`))
		case len(parts) == 5 && parts[2] == "actions" && parts[3] == "runs":
			out([]byte(runHeadJSON(parts[4], ".github/workflows/ci.yml", "main", repo)))
		case len(parts) == 4 && parts[2] == "issues":
			out([]byte(`{"number":` + parts[3] + `,"pull_request":{}}`))
		default:
			return RunResult{Exit: 1}
		}
		return RunResult{}
	}
	f.ran = append(f.ran, append([]string(nil), argv...))
	return RunResult{}
}

func (f *twoRepoGH) writes() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.ran) }

// bindScope rewrites the bound scope file as a launch or `yolo config apply` would, and the
// user config's broad ceiling.
func bindScope(t *testing.T, b *Broker, repos []string, onRequest string, grants ...brokerscope.Grant) {
	t.Helper()
	if err := brokerscope.ReplaceScopeContext(context.Background(), b.authority.path, b.authority.scope, repos, grants); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "packs = ['github']\n\n[allow.github]\nalways = ['read']\non_request = [" + onRequest + "]\n"
	if err := os.WriteFile(filepath.Join(dir, paths.UserConfigTOMLName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func serveOn(b *Broker, repo string, argv ...string) (int, string) {
	var errOut bytes.Buffer
	code := b.ServeContext(context.Background(), Request{Argv: argv, Repo: repo},
		paths.JailShortHash(b.authority.scope.Container), func([]byte) {}, func(p []byte) { errOut.Write(p) })
	return code, strings.Join(strings.Fields(errOut.String()), " ")
}

func readOn(repo string) brokerscope.Grant {
	return brokerscope.Grant{Kind: brokerscope.KindRead, Repo: "github.com/" + repo}
}
```

```go
// §8: runs is allowed for one identity only. The scope file's grant covers there, a grant on any
// other identity covers nothing, and deleting the user's table withdraws it at the next call.
func TestAScopedCeilingCoversItsIdentityOnlyAndNarrowsLive(t *testing.T) {
	writeUser := func(t *testing.T, scoped string) {
		t.Helper()
		dir := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		body := "packs = ['github']\n\n[allow.github]\nalways = ['read']\n" + scoped
		if err := os.WriteFile(filepath.Join(dir, paths.UserConfigTOMLName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const jail = "\n[repos.jail]\nrepo = 'github.com/o/r'\n\n[repos.jail.allow.github]\non_request = ['runs']\n"
	setup := func(t *testing.T) (*Broker, *twoRepoGH) {
		b, _ := windowFixture(t)
		gh := &twoRepoGH{}
		b.execute = gh.execute
		bindScope(t, b, []string{"o/r", "o/other"}, "", readOn("o/r"), readOn("o/other"),
			brokerscope.Grant{Kind: GrantRuns, Repo: "github.com/o/r", Workflow: "*"},
			// What a stale or hostile scope file would hold: the ceiling still refuses it.
			brokerscope.Grant{Kind: GrantRuns, Repo: "github.com/o/other", Workflow: "*"})
		writeUser(t, jail)
		return b, gh
	}
	t.Run("covers its identity", func(t *testing.T) {
		b, gh := setup(t)
		if code, msg := serveOn(b, "o/r", "run", "cancel", "55", "-R", "o/r"); code != 0 || gh.writes() != 1 {
			t.Fatalf("code %d, writes %d: %s", code, gh.writes(), msg)
		}
	})
	t.Run("covers no other identity", func(t *testing.T) {
		b, gh := setup(t)
		if code, msg := serveOn(b, "o/r", "run", "cancel", "55", "-R", "o/other"); code != ExitTempFail || gh.writes() != 0 {
			t.Fatalf("code %d, writes %d: %s", code, gh.writes(), msg)
		}
	})
	t.Run("narrows between the look and the transaction", func(t *testing.T) {
		b, gh := setup(t)
		afterGrantLook = func() { writeUser(t, "") }
		t.Cleanup(func() { afterGrantLook = nil })
		code, msg := serveOn(b, "o/r", "run", "cancel", "55", "-R", "o/r")
		if code == 0 || gh.writes() != 0 || !strings.Contains(msg, "runs grant no longer holds") {
			t.Fatalf("code %d, writes %d: %s", code, gh.writes(), msg)
		}
	})
}
```

  Same file:

```go
// The test file needs `context` and `encoding/json` among its imports.
// scopeFileGrants decodes grants as a scope file spells them, so a legacy grant is exactly what
// an older build wrote: no `repo_pattern` key.
func scopeFileGrants(t *testing.T, js string) []brokerscope.Grant {
	t.Helper()
	var grants []brokerscope.Grant
	if err := json.Unmarshal([]byte(js), &grants); err != nil {
		t.Fatal(err)
	}
	return grants
}

// A grant an older build's gate recorded on a pattern matched no command, so it stays inert in a
// broker that matches patterns; only a grant the gate approved as a pattern covers, behind the
// read floor and the live ceiling (GW-D80).
func TestOnlyAGrantApprovedAsAPatternCoversByPattern(t *testing.T) {
	const (
		legacyRuns  = `{"kind":"runs","repo":"github.com/o/*","workflow":"*"}`
		patternRuns = `{"kind":"runs","repo":"github.com/o/*","workflow":"*","repo_pattern":true}`
		otherRuns   = `{"kind":"runs","repo":"github.com/elsewhere/*","workflow":"*","repo_pattern":true}`
		readAny     = `{"kind":"read","repo":"*"}`
		legacyRead  = `{"kind":"read","repo":"github.com/o/*"}`
		patternRead = `{"kind":"read","repo":"github.com/o/*","repo_pattern":true}`
	)
	for _, c := range []struct {
		name, grants, onRequest string
		covered                 bool
	}{
		{"a legacy pattern grant, as recorded", "[" + readAny + "," + legacyRuns + "]", "'runs'", false},
		{"a legacy pattern read under an approved pattern write", "[" + legacyRead + "," + patternRuns + "]", "'runs'", false},
		{"a grant approved as a pattern", "[" + readAny + "," + patternRuns + "]", "'runs'", true},
		{"a pattern read and a pattern write", "[" + patternRead + "," + patternRuns + "]", "'runs'", true},
		{"a pattern that does not admit the repository", "[" + readAny + "," + otherRuns + "]", "'runs'", false},
		{"a pattern grant after the user withdrew the ceiling", "[" + readAny + "," + patternRuns + "]", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, gh := runsBroker(t)
			grantRuns(t, b, c.onRequest) // the user config; the scope file is replaced below
			if err := brokerscope.ReplaceScopeContext(context.Background(), b.authority.path, b.authority.scope,
				[]string{"o/*"}, scopeFileGrants(t, c.grants)); err != nil {
				t.Fatal(err)
			}
			code, msg := serveGH(b, "run", "cancel", "42228358413")
			if c.covered && (code != 0 || len(gh.writes()) != 1) {
				t.Fatalf("not covered: code %d, writes %v: %s", code, gh.writes(), msg)
			}
			if !c.covered && (code == 0 || len(gh.writes()) != 0) {
				t.Fatalf("covered: code %d, writes %v", code, gh.writes())
			}
		})
	}
}
```

  Red: `go test -count=1 -run 'TestAScopedCeiling|TestOnlyAGrantApprovedAsAPatternCoversByPattern' ./internal/config ./internal/ghbroker`.
  Predicted: the config tests stop at `scope check: fatal …` (no `repos.*.allow` row);
  `…/covers_its_identity` exits 75 where 0 is wanted; `TestOnlyAGrantApprovedAsAPatternCoversByPattern`: probed at `35224f8b0` through a
  `go test -overlay`, not kept in the tree, the four negative cases pass and the two positive cases
  fail, which pins today's inert meaning.
- **Change.**
  - Schema rows `repos.*.allow` (user, object) and `repos.*.allow.*` (user, `SchemaTypeAny`); no
    list row. The resolver validates `always` and `on_request` under the broker's table, and
    refuses them directly under `repos.<name>.allow`, naming `[allow.<source>]`.
  - Plan stage: a request stays alive when the broad lists lack the token and some user `repos`
    table for that source lists it. Gate stage (`ProduceReadableGateInput`): decide per identity
    with `brokerscope.Admits` plus host equality; with no admitting table, write the existing
    skipped status and `outsideCeilingReason`, and keep it in `brokeredSkippedRequests`' report at
    the launch and `yolo config apply`. `validateBrokeredRequests` (`brokeredskipped.go:111`)
    stays plan-only and reports a request no table anywhere lists.
  - Provenance names the admitting table: `repos.<name>.allow.<broker>.on_request` and its file.
  - A scoped `always` token lowers on the targets in this workspace's scope that its `repo`
    admits, as the broad `always` loop does on every target.
  - Run time: `BrokeredGrantKindsForRepo(source, repo string) (kinds []string, canonical bool, err error)`
    beside `BrokeredGrantKindsUsingReadablePolicy`, `repo` being `<host>/<owner>/<repo>`. It
    re-reads the user config at every call, returns the union of the broad lists and every user
    `[repos.<name>.allow.<source>]` whose `repo` admits the identity, resolves only this source's
    subtables (`ownAllowanceOnly`'s reason, `:1034`), counts a table with no `repo` or another
    host for nothing, treats a scoped table with no broad table as canonical, and gives no
    authority on any error. `userGrantCovers` (`ghbroker/workingset.go:114`) calls it with
    `SourceHost + "/" + t.Repo`. `gitbroker/policy.go:159` keeps the old call.
  - `grantRepoCovers(g brokerscope.Grant, repo string) bool` in `ghbroker/scope.go`: by
    `brokerscope.Admits` on `SourceHost` when `g.RepoPattern`, by `sameRepo` otherwise. It replaces
    `sameRepo` at `scope.go:71`, `workingset.go:107`, `runs.go:83` and `approverun.go:86`, and
    nowhere else. `scope.go:71` stops handing `sameRepo` to `brokerscope.ReadCovers`, whose matcher
    sees strings only; `unread` loops the grants itself, and `brokerscope.ReadCovers` and its five
    `gitbroker` callers are untouched. The caller validates a marked grant with
    `brokerscope.ValidGrant` before the match, because `brokerscope.ReadFile` does not validate.
    `AnyRepo` handling at each site stays.
  - Gate-stage ceiling for a pattern grant: inside when the broad lists hold the token, or a
    user table holds it and each of its segments is `*` or equals the workspace pattern's.
  - `UserBrokerAuthorityOf` carries the scoped tables in its result, and
    `configmigrate/brokered.go:41` puts them in the proof's user entry, so two user configs whose
    scoped ceilings differ never compare equal.
- **Gates.** Task 2's command.
- **Fences.** `internal/config`; in `internal/ghbroker`, `workingset.go`, the four matcher sites
  and the new test file; `internal/configmigrate/brokered.go`. Not `sameRepo`, `runsCovered`, `approveRunCovered`, `runsOf`, the
  classifier's refusals, `internal/brokerstore` or `internal/gitbroker`.
- **Commit.** `feat(config,ghbroker): repository-scoped ceilings, additive and checked live (GW-D64, GW-D79)`.

### Task 4: stored-authority regression tests (after 3; test-only)

- **Settled.** Item 12, and from the design: "The build must confirm such an approval is not
  effective." Read at `8cf2c548a`: the approval record is a comparison baseline only and is
  replaced at each approval (`compareScope`); a launch hands its broker the gate's in-memory
  result (`recordApprovedScopes`, `preflight.go:416` → `writeScopeFiles`, `brokeredscope.go:185`);
  config-derived write grants match by exact identity (`userGrantCovers`, `runsGrantHeld`,
  `approveRunGrantHeld`); every stored answer passes `Authority.Classify`'s scope and read floor.
- **Tests.** Append to `internal/ghbroker/repobinding_test.go`. Both are **pins**:

```go
// GW-D79: origin holds every workflow, approve-run and create-pr; a second repository is in
// scope to read. Each command runs unasked on origin and asks on the second.
func TestOriginsGrantsDoNotReachASecondRepositoryInScope(t *testing.T) {
	const bot = "github-actions[bot]"
	for _, c := range []struct {
		name string
		argv func(repo string) []string
	}{
		{"dispatch", func(r string) []string { return []string{"workflow", "run", "ci.yml", "-R", r} }},
		{"cancel", func(r string) []string { return []string{"run", "cancel", "55", "-R", r} }},
		{"rerun", func(r string) []string { return []string{"run", "rerun", "55", "-R", r} }},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, _ := windowFixture(t)
			gh := &twoRepoGH{}
			b.execute = gh.execute
			bindScope(t, b, []string{"o/r", "o/other"}, "'runs', 'approve_runs', 'create_pr'",
				readOn("o/r"), readOn("o/other"),
				brokerscope.Grant{Kind: GrantRuns, Repo: "github.com/o/r", Workflow: "*"},
				brokerscope.Grant{Kind: GrantApproveRun, Repo: "github.com/o/r", Actor: bot},
				brokerscope.Grant{Kind: GrantCreatePR, Repo: "github.com/o/r"})
			if code, msg := serveOn(b, "o/r", c.argv("o/other")...); code != ExitTempFail || gh.writes() != 0 {
				t.Fatalf("origin's grant reached o/other: code %d, writes %d: %s", code, gh.writes(), msg)
			}
			if got := pending(t, b); len(got) != 1 || got[0].Repo != "o/other" {
				t.Fatalf("the uncovered write filed %+v, want one request on o/other", got)
			}
			if code, msg := serveOn(b, "o/r", c.argv("o/r")...); code != 0 || gh.writes() != 1 {
				t.Fatalf("origin's own grant did not cover: code %d, writes %d: %s", code, gh.writes(), msg)
			}
		})
	}
}
```

```go
// A lasting working-set entry and a timed one on a repository the config no longer writes to
// keep covering their own resource while the repository is readable in scope, stop when it
// loses its read grant or leaves the scope, and are never ended by either change.
func TestStoredEntriesFollowTheLiveScopeAndTheReadFloor(t *testing.T) {
	b, _ := windowFixture(t)
	gh := &twoRepoGH{}
	b.execute = gh.execute
	b.store.Entries = RequestEntries
	both := []string{"o/r", "o/other"}
	bindScope(t, b, both, "", readOn("o/r"), readOn("o/other")) // no write grant anywhere
	lasting, _, err := b.store.AddWorking(brokerstore.WorkingEntry{Workspace: b.workspace,
		Repo: "github.com/o/other", Kind: brokerstore.WorkingPR, Resource: "#7", By: GrantCreatePR})
	if err != nil {
		t.Fatal(err)
	}
	comment := []string{"pr", "comment", "7", "--body", "x", "-R", "o/other"}
	if code, msg := serveOn(b, "o/r", comment...); code != 0 || gh.writes() != 1 {
		t.Fatalf("a readable in-scope entry did not cover: code %d, writes %d: %s", code, gh.writes(), msg)
	}
	// The same number on origin is another resource.
	if code, _ := serveOn(b, "o/r", "pr", "comment", "7", "--body", "x", "-R", "o/r"); code != ExitTempFail || gh.writes() != 1 {
		t.Fatalf("the entry crossed repositories: code %d, writes %d", code, gh.writes())
	}
	before := len(pending(t, b))

	// Read withdrawn for o/other only: in scope, not readable.
	bindScope(t, b, both, "", readOn("o/r"))
	if code, msg := serveOn(b, "o/r", comment...); code == 0 || gh.writes() != 1 || len(pending(t, b)) != before {
		t.Fatalf("an entry outranked the read floor: code %d, writes %d: %s", code, gh.writes(), msg)
	}
	// Out of scope entirely.
	bindScope(t, b, []string{"o/r"}, "", readOn("o/r"))
	if code, msg := serveOn(b, "o/r", comment...); code == 0 || gh.writes() != 1 {
		t.Fatalf("an entry outranked the scope: code %d, writes %d: %s", code, gh.writes(), msg)
	}
	if e, live, err := b.store.LiveWorkingKind(b.workspace, "github.com/o/other", "#7", brokerstore.WorkingPR); err != nil || !live || e.ID != lasting.ID {
		t.Fatalf("a scope change ended the entry: %+v live=%v err=%v", e, live, err)
	}
	// Back in scope through a gate: the human's record covers again.
	bindScope(t, b, both, "", readOn("o/r"), readOn("o/other"))
	if code, msg := serveOn(b, "o/r", comment...); code != 0 || gh.writes() != 2 {
		t.Fatalf("a restored scope did not restore coverage: code %d, writes %d: %s", code, gh.writes(), msg)
	}
}
```

  New `internal/cli/run/brokeredscope_repobinding_test.go`, modelled on
  `TestAReadableRequestedRepoIsApprovedAtTheGateAndReachesTheBroker`. The `brokered` block's four
  kinds and `kind_capabilities` are literals copied from
  `packs/github/loopholes/github-broker/loophole.toml:56-59`. Fixture: `.git/config` with
  `origin = https://github.com/o/r.git`; user `[allow.github] always = ["read"]`,
  `on_request = ["runs", "approve_runs"]`; workspace file:

```toml
[requests.github]
runs = ["*"]
approve_runs = ["github-actions[bot]"]

[repos.notes]
repo = "github.com/o/other"
```

  Seed the record as the fan-out build left it, through `config.RecordApproval(ws, cfg, check)`
  with a `ScopeCheck` whose `github` source holds read on both identities plus `runs *` and
  `approve-run` on both. `TestAStaleApprovalRecordNeverBecomesAScopeFile`, four subtests:
  1. `AcceptConfigChanges = true`: the gate output holds `- grant runs github.com/o/other` and
     `removed`; `readHandedScope(…).Grants` holds read on both, `runs` and `approve-run` with
     `Repo == "github.com/o/r"`, and no non-read grant whose `Repo` is `github.com/o/other` or
     `*`; `config.ApprovedGrants(ws, "github")` lacks the removed strings.
  2. No terminal, no accept: `checkConfigChanges` is false, no scope file is handed, the record
     still holds the old strings.
  3. After 1, `origin` becomes `https://github.com/o/new.git`. Without accept the gate refuses
     and shows `+ grant runs github.com/o/new` `added` and `- grant runs github.com/o/r`
     `removed`. With accept the scope file holds no grant naming `o/r`.
  4. `[repos.notes.requests.github] runs = ["a.yml"]` approved on `o/other`; a local file sets
     `repo = "github.com/o/third"`. The shape of 3 for `o/other` → `o/third`.

  In `internal/cli/run/liveapply_test.go`, `TestConfigApplyRemovesAGrantTheGateNoLongerApproves`,
  the mirror of `TestConfigApplyReplacesGrants` (`:912`) with its helpers: the live file holds
  `{create-branch, github.com/o/r}`, the workspace asks for none; `replaceScopeReposSeam`
  receives grants without it and the output holds `-grant create-branch github.com/o/r` and
  `applied now`. A **pin**: this is the only path that revokes a config-derived grant in a
  running jail.

  Run: `go test -count=1 -run 'TestOriginsGrants|TestStoredEntries|TestAStaleApprovalRecord|TestConfigApplyRemovesAGrant' ./internal/ghbroker ./internal/cli/run`.
  All green on the Task 3 tree. A red is a stop: report the command, the exit code and the
  message, and change no production file.
- **Retain as they are.** `TestExactGrantDoesNotCrossRepositoryOrScope`,
  `TestExactGrantOutlivesItsBrokerForItsWorkspaceOnly`,
  `TestAReadGrantWithdrawnDuringTheWaitRefusesTheWrite`,
  `TestANarrowedScopeFileRefusesARemovedRepositoryAtTheNextRequest`,
  `TestRerunGrantWithdrawnBeforeTheTransactionAsks`,
  `TestAUserGrantWithdrawnBeforeTheTransactionAsks`, `TestCreatePRGrantNeedsTheUsersAllowance`,
  `TestWindowedWriteDoesNotSpendLegacySetWideGrant`.
- **Fences.** Test files only. No GitHub call, no sleep as a pass condition, no pack file read.
- **Commit.** `test(ghbroker,run): stored approvals follow the live scope, never a stale record (GW-D79)`.

### Task 5: the verbs (after 3)

- **Settled.** GW-D77: `yolo request repos <alias> <host>/<path> [--local]`;
  `yolo request repos.<alias>.<broker> <capability> [resource]`; `yolo allow repos <alias> <host>/<path>`;
  `yolo allow repos.<alias>.<broker> [always|on_request] <capability>`. The user form takes
  capability names, never a resource. `repos` is read as the first word ahead of every source
  and pack. Items 5, 9. A qualified request above the ceiling is written and reported skipped,
  naming the scoped allow first.
- **Tests first.** New `internal/cli/requestrepos_test.go`. They enter through `Main`:

```go
package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

const requestReposAllow = "[allow.git]\nalways = [\"fetch\"]\n\n[allow.github]\nalways = [\"read\"]\non_request = [\"runs\", \"create_pr\"]\n"

func TestRequestReposDeclaresANamedRepository(t *testing.T) {
	fx := newNamedVerbFixture(t, "", requestReposAllow)
	userBefore, fragmentBefore := readFile(t, fx.userConfig), readFile(t, fx.fragment)

	rc, out, errs := captureStd(t, func() int {
		return Main([]string{"yolo", "request", "repos", "infra", "github.com/acme/infra"})
	})
	assertPublicPostWriteCheck(t, rc, out, errs)
	got := readFile(t, fx.project)
	if !strings.Contains(got, "[repos.infra]") || !strings.Contains(got, `repo = "github.com/acme/infra"`) {
		t.Fatalf("project file was not written:\n%s\nstdout=%q stderr=%q", got, out, errs)
	}
	if strings.Contains(got, "[requests") {
		t.Fatalf("declaring a repository wrote a request:\n%s", got)
	}
	if !strings.Contains(out, "yolo config apply") {
		t.Fatalf("the verb did not name the host step: %q", out)
	}

	local := filepath.Join(fx.workspace, config.WorkspaceLocalTOMLConfigName)
	rc, out, errs = captureStd(t, func() int {
		return Main([]string{"yolo", "request", "repos", "notes", "github.com/acme/notes", "--local"})
	})
	assertPublicPostWriteCheck(t, rc, out, errs)
	if l := readFile(t, local); !strings.Contains(l, "[repos.notes]") || !strings.Contains(l, `repo = "github.com/acme/notes"`) {
		t.Fatalf("--local did not write the local file:\n%s", l)
	}
	if strings.Contains(readFile(t, fx.project), "notes") {
		t.Fatal("--local wrote the project file")
	}
	if readFile(t, fx.userConfig) != userBefore || readFile(t, fx.fragment) != fragmentBefore {
		t.Fatal("yolo request edited the user config")
	}

	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"yolo", "request", "repos", "infra2", "acme/infra"}, []string{"github.com/acme/infra", "yolo request repos infra2"}},
		{[]string{"yolo", "request", "repos", "bad", "*/acme/infra"}, []string{"host", "yolo request repos bad"}},
		{[]string{"yolo", "request", "repos", "bad", "github.com/ac*/infra"}, []string{"whole", "yolo request repos bad"}},
		{[]string{"yolo", "request", "repos", "infra"}, []string{"yolo request repos <alias> <host>/<path>"}},
	} {
		before := readFile(t, fx.project)
		rc, out, errs := captureStd(t, func() int { return Main(tc.args) })
		if rc == 0 {
			t.Fatalf("%v rc=0; stdout=%q stderr=%q", tc.args, out, errs)
		}
		for _, want := range tc.want {
			if !strings.Contains(errs, want) {
				t.Fatalf("%v refusal lacks %q: %q", tc.args, want, errs)
			}
		}
		if readFile(t, fx.project) != before {
			t.Fatalf("%v was refused and still wrote the project file", tc.args)
		}
	}
}

func TestRequestReposAliasWritesTheQualifiedLine(t *testing.T) {
	fx := newNamedVerbFixture(t, "[repos.infra]\nrepo = \"github.com/acme/infra\"\n", requestReposAllow)

	rc, out, errs := captureStd(t, func() int {
		return Main([]string{"yolo", "request", "repos.infra.github", "runs", "deploy.yml"})
	})
	assertPublicPostWriteCheck(t, rc, out, errs)
	rc, out, errs = captureStd(t, func() int {
		return Main([]string{"yolo", "request", "repos.infra.github", "create_pr"})
	})
	assertPublicPostWriteCheck(t, rc, out, errs)
	got := readFile(t, fx.project)
	for _, want := range []string{"[repos.infra.requests.github]", `runs = ["deploy.yml"]`, "create_pr = true"} {
		if !strings.Contains(got, want) {
			t.Fatalf("project file lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "[requests.github]") {
		t.Fatalf("a qualified request wrote the unqualified table:\n%s", got)
	}
	if strings.Contains(out, "runs without it") {
		t.Fatalf("an allowed request was reported as skipped: %q", out)
	}

	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"yolo", "request", "repos.nope.github", "runs", "deploy.yml"}, []string{"yolo request repos nope <host>/<path>"}},
		{[]string{"yolo", "request", "repos.origin.github", "create_pr"}, []string{"yolo request github create_pr"}},
		{[]string{"yolo", "request", "repos.infra.github", "runs"}, []string{"workflow file name", "yolo request repos.infra.github runs nightly.yml"}},
		{[]string{"yolo", "request", "repos.infra.github", "pushh"}, []string{"create_pr", "runs", "yolo request repos.infra.github"}},
		{[]string{"yolo", "request", "repos.infra.github", "read"}, []string{"yolo allow github read"}},
		{[]string{"yolo", "request", "repos.infra.github", "repos", "acme/x"}, []string{"yolo request repos <alias> <host>/<path>"}},
		{[]string{"yolo", "request", "repos.infra", "push", "main"}, []string{"yolo request git push main"}},
	} {
		before := readFile(t, fx.project)
		rc, out, errs := captureStd(t, func() int { return Main(tc.args) })
		if rc != 1 {
			t.Fatalf("%v rc=%d, want 1; stdout=%q stderr=%q", tc.args, rc, out, errs)
		}
		for _, want := range tc.want {
			if !strings.Contains(errs, want) {
				t.Fatalf("%v refusal lacks %q: %q", tc.args, want, errs)
			}
		}
		if readFile(t, fx.project) != before {
			t.Fatalf("%v was refused and still wrote the project file", tc.args)
		}
	}
}

// A qualified request above the ceiling is written and reported skipped, naming the scoped allow
// first: the broad one would allow it on every repository.
func TestRequestReposAliasOutsideTheCeilingNamesTheScopedAllow(t *testing.T) {
	fx := newNamedVerbFixture(t, "[repos.infra]\nrepo = \"github.com/acme/infra\"\n", "[allow.github]\nalways = [\"read\"]\n")
	rc, out, errs := captureStd(t, func() int {
		return Main([]string{"yolo", "request", "repos.infra.github", "runs", "deploy.yml"})
	})
	assertPublicPostWriteCheck(t, rc, out, errs)
	if got := readFile(t, fx.project); !strings.Contains(got, `runs = ["deploy.yml"]`) {
		t.Fatalf("a request above the ceiling was not written:\n%s", got)
	}
	for _, want := range []string{"runs without it", "yolo allow repos <alias> github.com/acme/infra", "yolo allow repos.<alias>.github on_request runs"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the skip report lacks %q: %q", want, out)
		}
	}
}

func TestConfigWhyNamesTheReposVerbs(t *testing.T) {
	newNamedVerbFixture(t, "", "")
	for key, want := range map[string]string{
		"repos.infra.repo":                         "yolo request repos infra <host>/<path>",
		"repos.infra.requests.github.runs":         "set it with `yolo request repos.infra.github runs <workflow file>`",
		"repos.infra.requests.github.create_pr":    "set it with `yolo request repos.infra.github create_pr`",
		"repos.infra.requests.github.approve_runs": "set it with `yolo request repos.infra.github approve_runs <actor login>`",
		"repos.mine.allow.github.on_request":       "set it with `yolo allow repos.mine.github on_request <capability>`",
	} {
		rc, out, errs := captureStd(t, func() int { return Main([]string{"yolo", "config", "why", key}) })
		if rc != 0 || !strings.Contains(out, want) {
			t.Fatalf("config why %s rc=%d stdout=%q stderr=%q; want %q", key, rc, out, errs, want)
		}
		if strings.Contains(out, "yolo config set") {
			t.Fatalf("config why %s offers the generic verb beside the named one: %q", key, out)
		}
	}
}
```

  New `internal/cli/allowrepos_test.go`:

```go
package cli

import (
	"strings"
	"testing"
)

func TestAllowReposDeclaresAUserNameAndItsScopedCeiling(t *testing.T) {
	fx := newNamedVerbFixture(t, "[requests.github]\nruns = [\"nightly.yml\"]\n", "[allow.github]\nalways = [\"read\"]\n")
	projectBefore := readFile(t, fx.project)
	rec := recordAllowGate(t, false)
	user := func() string { return readFile(t, fx.userConfig) + "\n" + readFile(t, fx.fragment) }

	rc, out, errs := captureStd(t, func() int {
		return Main([]string{"yolo", "allow", "repos", "mine", "github.com/mschulkind/*"})
	})
	if rc != 0 {
		t.Fatalf("yolo allow repos rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	if got := user(); !strings.Contains(got, "[repos.mine]") || !strings.Contains(got, `repo = "github.com/mschulkind/*"`) {
		t.Fatalf("the user config holds no [repos.mine]:\n%s", got)
	}

	rc, out, errs = captureStd(t, func() int {
		return Main([]string{"yolo", "allow", "repos.mine.github", "on_request", "runs"})
	})
	if rc != 0 {
		t.Fatalf("yolo allow repos.mine.github rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	got := user()
	if !strings.Contains(got, "[repos.mine.allow.github]") || !strings.Contains(got, `on_request = ["runs"]`) {
		t.Fatalf("the user config holds no scoped ceiling:\n%s", got)
	}
	if strings.Contains(got, "[allow.github]\nalways = [\"read\"]\non_request") {
		t.Fatalf("a scoped allow widened the broad ceiling:\n%s", got)
	}
	if !strings.Contains(out, "yolo config apply") || rec.calls != 0 {
		t.Fatalf("with no terminal the verb names the command and runs no gate: calls=%d stdout=%q", rec.calls, out)
	}
	if readFile(t, fx.project) != projectBefore {
		t.Fatal("yolo allow edited the project file: a user name must put nothing in a workspace's scope")
	}

	for _, tc := range []struct {
		args []string
		rc   int
		want []string
	}{
		{[]string{"yolo", "allow", "repos.mine.github", "on_request", "runs", "deploy.yml"}, 2, []string{"usage: yolo allow"}},
		{[]string{"yolo", "allow", "repos.nope.github", "on_request", "runs"}, 1, []string{"yolo allow repos nope <host>/<path>"}},
		{[]string{"yolo", "allow", "repos.mine.github", "on_request", "runz"}, 1, []string{"runs", "yolo allow repos.mine.github"}},
		{[]string{"yolo", "allow", "repos", "mine2", "mschulkind/*"}, 1, []string{"github.com/mschulkind/*"}},
		{[]string{"yolo", "allow", "repos", "mine2", "*/mschulkind/x"}, 1, []string{"host"}},
	} {
		before := user()
		rc, out, errs := captureStd(t, func() int { return Main(tc.args) })
		if rc != tc.rc {
			t.Fatalf("%v rc=%d, want %d; stdout=%q stderr=%q", tc.args, rc, tc.rc, out, errs)
		}
		for _, want := range tc.want {
			if !strings.Contains(errs, want) {
				t.Fatalf("%v refusal lacks %q: %q", tc.args, want, errs)
			}
		}
		if user() != before {
			t.Fatalf("%v was refused and still wrote the user config", tc.args)
		}
	}
}
```

  Red: `go test -count=1 -run 'TestRequestRepos|TestAllowRepos|TestConfigWhyNamesTheReposVerbs' ./internal/cli`.
  Predicted: `request repos infra …` fails in `findDeclaredBroker` with
  `no selected pack declares a broker source or grant kinds named "repos"`, so the first test dies
  at `assertPublicPostWriteCheck`; `allow repos mine …` exits non-zero the same way; `config why`
  prints the generic `yolo config set repos.…`.
- **Change.** In `requestRunIn` (`request.go:56`) and `allowRunW`, classify `positionals[0]`
  before `allowSelectedBrokers`' result is consulted: `repos` is the declaration word;
  `repos.<alias>.<source>` re-enters the existing capability switch with the prefix
  `repos.<alias>.requests.<source>.` or `repos.<alias>.allow.<source>.`; `repos.<alias>` alone is
  the Git form, refused per item 9. An alias the target scope does not declare, and for
  `yolo request` that is no remote's name, is refused naming the declaration verb. `--local` is
  accepted by `yolo request` for the `repos` words only. Extend `requestUsage`, `allowUsage`
  (`subhelp.go:139-140` reads them) and `configWhySetCommand` (`configwhy.go:409`).
- **Also.** `configreftoml_test.go`: `"repos"` in `TestConfigRefTOMLSectionNamesOnlyReadableKeys`'s
  list; `"yolo request repos"` and `"yolo allow repos"` in
  `TestConfigRefTOMLSectionNamesTheVerbsTheCLIHas`. `config_ref.txt`'s TOML section gains the two
  verb lines and the `[repos.<name>]` table. Run `rg -n 'yolo (request|allow) ' internal/cli/brokerhintverbs_test.go internal/cli/advicecommands_test.go`;
  a verb list there that enumerates words gains the new ones.
- **Cheap.** Helper names; whether the qualified verbs live in the existing files or new ones;
  wording beyond the tested tokens.
- **Gates.** `go test -count=1 ./internal/cli/... ./internal/config`.
- **Fences.** `internal/cli` and `config_ref.txt`. No schema row, no resolver, no `packs/`. A
  refusal that needs a resolver change is a stop.
- **Commit.** `feat(cli): yolo request repos and yolo allow repos (GW-D77)`.

### Task 6: the migration (after 5)

- **Settled.** Item 11. "Each grant that today reaches the listed repository is written as an
  explicit line in its named table and printed; the before and after grant sets are equal."
  "The migration prints every retained named write with its identity and the file and key it
  came from."
- **What converts**, through `projectBrokerDeclaration(source)`, named-host sources only:

| Old | New |
| :--- | :--- |
| TOML or JSONC `requests.<S>.repos = ["o/r"]` | `[repos.<name>] repo = "<host>/o/r"`, and under `[repos.<name>.requests.<S>]` a copy of every non-read, non-`repos` line the **merged workspace** writes under `[requests.<S>]`. The entry leaves the list; an emptied list is removed |
| each remote on the host other than `origin`, when the file set holds an old spelling and the merged workspace writes an unqualified non-read line | `[repos.<remote name>.requests.<S>]` with a copy of each such line and no `repo` line, in the file the conversion already writes (the one holding the old key; for a JSONC conversion, the converted file holding the lines). A remote named `all` gets a declared `owner-repo` table instead |
| JSONC `brokered.<S>.repos`, concrete entry | the same table, `repo` as written, with the same copied lines when the merged workspace writes unqualified `requests.<S>` lines (the readable path fans them onto the entry today, `collectLegacyReadableRepositories`, `readablescope.go:253`); replaces the placement at `lower.go:512` |
| JSONC `brokered.<S>.repos` pattern entry, and `brokered.<S>.grants[i]` whose `repo` is a pattern | **not converted**: no table is written for it, its grants are not copied, and its old file is kept and still read, with the reason and next step of [GW-D80](github-write-scopes.md#GW-D80). Copying a write line onto a pattern table is dropped: it would make live authority equal to an inert grant |
| JSONC `brokered.<S>.grants[i]`, `repo` is origin's identity | unqualified `requests.<S>.<capability>`, whatever the remote count; replaces `soleRemote` for named-host sources |
| JSONC `brokered.<S>.grants[i]`, another concrete `repo` | `[repos.<name>]` declared if absent, the line under `[repos.<name>.requests.<S>]` |
| JSONC `brokered.<S>.grants[i]`, `repo = "*"` | not converted, today's `grantScopeReason` text and old file kept |
| anything for `AnyRemoteHost` (git) | unchanged; `soleRemote` and `grantScopeReason` as today |

  The name is the repository's last segment with each character outside `[A-Za-z0-9_-]`
  replaced by `-` (`OWNER/*` takes the owner, `*/REPO` the repository, `*/*` takes `every`); `owner-repo` when that is taken by another identity in the workspace's
  tables, equals a remote of this clone that points elsewhere, or is `all`. A name still taken is
  not converted, the reason naming `yolo request repos <alias> <host>/<path>`. A TOML
  `requests.<S>.repos` entry cannot hold a wildcard: the resolver refuses it today
  (`readableallow.go:523`).

  A TOML project or local file holding the old key takes `RoleRespell`, is rewritten in place by
  byte-preserving edits, and is listed by `Graph.Rewritten`, so a TOML-only workspace is not
  short-circuited to "equal, nothing to do" (`plan.go:~372`).
- **Tests first.** New `internal/configmigrate/reposbinding_test.go`:

```go
package configmigrate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// namedRemotes writes the workspace's git config with the remotes named as given, in order:
// name, url, name, url. gitRemotes names them ra, rb, which no unqualified request reaches.
func namedRemotes(t *testing.T, ws string, pairs ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("[core]\n\tbare = false\n")
	for i := 0; i+1 < len(pairs); i += 2 {
		b.WriteString("[remote \"" + pairs[i] + "\"]\n\turl = " + pairs[i+1] + "\n")
	}
	write(t, filepath.Join(ws, ".git", "config"), b.String())
	write(t, filepath.Join(ws, ".git", "HEAD"), "ref: refs/heads/main\n")
}

const reposUserTOML = "packs = [\"git\", \"github\"]\n\n[allow.github]\nalways = [\"read\"]\non_request = [\"runs\", \"approve_runs\", \"create_pr\"]\n"

// The real shape: the project file asks, the local file lists the extra repository. Today the
// listed repository takes every project line; the migration writes each as its own line.
func TestMigrateTurnsATOMLReposListIntoANamedTableWithItsRetainedWrites(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.toml"), reposUserTOML)
	ws := realDir(t)
	namedRemotes(t, ws, "origin", "https://github.com/acme/app.git")
	project := "[requests.github]\nruns = [\"nightly.yml\", \"release.yml\"]\napprove_runs = [\"github-actions[bot]\"]\n"
	write(t, filepath.Join(ws, "yolo-jail.toml"), project)
	localPath := filepath.Join(ws, "yolo-jail.local.toml")
	write(t, localPath, "# mine\n[requests.github]\nrepos = [\"other/lib\"]\n")

	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if !p.Equal || len(p.NotConverted) > 0 {
		t.Fatalf("plan unequal or incomplete: %+v %+v", p.Differences, p.NotConverted)
	}
	if len(p.Targets) != 1 {
		t.Fatalf("targets = %+v; want the local file alone", p.Targets)
	}
	local := target(t, p, localPath)
	if local.New != localPath {
		t.Fatalf("a TOML file is rewritten in place; New = %s", local.New)
	}
	text := string(local.Bytes)
	for _, want := range []string{"# mine", "[repos.lib]", `repo = "github.com/other/lib"`, "[repos.lib.requests.github]",
		`runs = ["nightly.yml", "release.yml"]`, `approve_runs = ["github-actions[bot]"]`} {
		if !strings.Contains(text, want) {
			t.Fatalf("the rewritten local file lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "repos = [") {
		t.Fatalf("the old list survived:\n%s", text)
	}
	for _, want := range []string{`"runs github.com/other/lib nightly.yml"`, `"runs github.com/other/lib release.yml"`,
		`"approve-run github.com/other/lib github-actions[bot]"`, `"runs github.com/acme/app nightly.yml"`} {
		if !strings.Contains(p.canonical.grants, want) {
			t.Fatalf("the proof's grants lack %s: %s", want, p.canonical.grants)
		}
	}
	transcript := p.Transcript()
	for _, want := range []string{"github.com/other/lib", "repos.lib.requests.github.runs", "yolo-jail.local.toml"} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("the transcript does not print the retained write (%s):\n%s", want, transcript)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(ws, "yolo-jail.toml")); string(got) != project {
		t.Fatalf("planning wrote the project file:\n%s", got)
	}
}

// A legacy grant names its repository, so it never fanned out: origin's converts to the
// unqualified line whatever the remote count, any other to its own table, and neither gains the
// other's line.
func TestMigratePlacesAnExplicitRepositoryGrantOnItsOwnRepository(t *testing.T) {
	env(t)
	ws := realDir(t)
	namedRemotes(t, ws, "origin", "https://github.com/acme/app.git", "upstream", "https://github.com/acme/up.git")
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"brokered": {"github": {"repos": ["other/lib"], "grants": [
		{"kind": "runs", "repo": "github.com/acme/app", "workflow": "a.yml"},
		{"kind": "runs", "repo": "github.com/other/lib", "workflow": "b.yml"}
	]}}}`+"\n")
	p := plan(t, Request{Workspace: ws})
	if !p.Equal || len(p.NotConverted) > 0 {
		t.Fatalf("plan unequal or incomplete: %+v %+v", p.Differences, p.NotConverted)
	}
	text := targetText(p)
	for _, want := range []string{"[requests.github]", `runs = ["a.yml"]`, "[repos.lib]", `repo = "github.com/other/lib"`,
		"[repos.lib.requests.github]", `runs = ["b.yml"]`} {
		if !strings.Contains(text, want) {
			t.Fatalf("the converted file lacks %q:\n%s", want, text)
		}
	}
	for _, want := range []string{`"runs github.com/acme/app a.yml"`, `"runs github.com/other/lib b.yml"`} {
		if !strings.Contains(p.canonical.grants, want) {
			t.Fatalf("the proof's grants lack %s: %s", want, p.canonical.grants)
		}
	}
	for _, never := range []string{"github.com/other/lib a.yml", "github.com/acme/app b.yml", "runs github.com/acme/up"} {
		if strings.Contains(p.canonical.grants, never) {
			t.Fatalf("the conversion widened: %s in %s", never, p.canonical.grants)
		}
	}
}

// A grant on every repository covers repositories nobody has declared yet, so copying it to the
// declared names is not equal to it: it stays in the old file, with its next step. A pin, green
// today; it fails if the wildcard is ever copied to the current names.
func TestMigrateLeavesALegacyWildcardRepositoryGrantInTheOldFile(t *testing.T) {
	env(t)
	ws := realDir(t)
	namedRemotes(t, ws, "origin", "https://github.com/acme/app.git")
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"brokered": {"github": {"repos": ["other/lib"], "grants": [
		{"kind": "runs", "repo": "*", "workflow": "a.yml"}
	]}}}`+"\n")
	g, err := Discover(Request{Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	p, err := Plan(g)
	var refusal *InequalityError
	if errors.As(err, &refusal) {
		p = refusal.Plan
	} else if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(p.NotConverted) != 1 || !strings.Contains(p.NotConverted[0].Reason, `every repository ("*")`) ||
		!strings.Contains(p.NotConverted[0].Reason, "leave the old file") {
		t.Fatalf("not converted = %+v", p.NotConverted)
	}
	if text := targetText(p); strings.Contains(text, "a.yml") {
		t.Fatalf("the wildcard grant was copied to a current name:\n%s", text)
	}
}

// The link moves as a link and the file it names is never written (RC-D41), for a TOML file
// rewritten in place as for a converted one.
func TestMigrateRewritesALinkedTOMLFileWithoutTouchingItsTarget(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.toml"), reposUserTOML)
	ws := realDir(t)
	namedRemotes(t, ws, "origin", "https://github.com/acme/app.git")
	dots := filepath.Join(home, "dotfiles")
	body := "[requests.github]\nrepos = [\"other/lib\"]\n"
	write(t, filepath.Join(dots, "local.toml"), body)
	linkPath, linkText := filepath.Join(ws, "yolo-jail.local.toml"), filepath.Join(dots, "local.toml")
	if err := os.Symlink(linkText, linkPath); err != nil {
		t.Fatal(err)
	}
	dotsBefore := treeState(t, dots)

	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	tg := target(t, p, linkPath)
	if tg.LinkText != linkText || tg.Backup != linkPath+".pre-migrate" || tg.New != linkPath {
		t.Fatalf("target = %+v", tg)
	}
	if _, err := Apply(p, ApplyOptions{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	info, err := os.Lstat(linkPath)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("the rewritten file is not a regular file: %v %v", info, err)
	}
	if got, err := os.Readlink(linkPath + ".pre-migrate"); err != nil || got != linkText {
		t.Fatalf("the link was not kept as a link: %q %v", got, err)
	}
	after := treeState(t, dots)
	if len(after) != len(dotsBefore) {
		t.Fatalf("the link's directory changed: %v", after)
	}
	for name, state := range dotsBefore {
		if after[name] != state {
			t.Fatalf("the link's target %s was touched", name)
		}
	}
}
```

  Same file:

```go
// Before GW-D79 every unqualified line also reached each other remote. A converted workspace
// keeps those writes as lines of a table named for the remote, printed like the rest.
func TestMigrateKeepsTheWritesAnotherRemoteHad(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.toml"), reposUserTOML)
	ws := realDir(t)
	namedRemotes(t, ws, "origin", "https://github.com/acme/app.git", "upstream", "https://github.com/acme/up.git")
	project := "[requests.github]\nruns = [\"nightly.yml\"]\n"
	write(t, filepath.Join(ws, "yolo-jail.toml"), project)
	localPath := filepath.Join(ws, "yolo-jail.local.toml")
	write(t, localPath, "[requests.github]\nrepos = [\"other/lib\"]\n")

	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if !p.Equal || len(p.NotConverted) > 0 {
		t.Fatalf("plan unequal or incomplete: %+v %+v", p.Differences, p.NotConverted)
	}
	text := string(target(t, p, localPath).Bytes)
	for _, want := range []string{"[repos.upstream.requests.github]", "[repos.lib.requests.github]", `repo = "github.com/other/lib"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("the rewritten local file lacks %q:\n%s", want, text)
		}
	}
	if strings.Count(text, `runs = ["nightly.yml"]`) != 2 {
		t.Fatalf("want the line once under each table:\n%s", text)
	}
	if strings.Contains(text, "github.com/acme/up") {
		t.Fatalf("a remote's table was given a repo line:\n%s", text)
	}
	for _, want := range []string{`"runs github.com/acme/app nightly.yml"`, `"runs github.com/acme/up nightly.yml"`, `"runs github.com/other/lib nightly.yml"`} {
		if !strings.Contains(p.canonical.grants, want) {
			t.Fatalf("the proof's grants lack %s: %s", want, p.canonical.grants)
		}
	}
	transcript := p.Transcript()
	for _, want := range []string{"github.com/acme/up", "repos.upstream.requests.github.runs"} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("the transcript does not print the retained write (%s):\n%s", want, transcript)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(ws, "yolo-jail.toml")); string(got) != project {
		t.Fatalf("planning wrote the project file:\n%s", got)
	}
}

// A legacy pattern entry is not converted: a table for it would be a live grant equal to an inert
// one (GW-D80). A pin: red on the current tree, which converts it.
func TestMigrateLeavesALegacyPatternEntryInTheOldFile(t *testing.T) {
	env(t)
	ws := realDir(t)
	namedRemotes(t, ws, "origin", "https://github.com/acme/app.git")
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"brokered": {"github": {"repos": ["acme/*"]}}}`+"\n")
	p := plan(t, Request{Workspace: ws})
	if len(p.NotConverted) == 0 {
		t.Fatalf("the pattern entry was converted, want it named as not converted: %+v", p)
	}
	if text := targetText(p); strings.Contains(text, "[repos.acme]") {
		t.Fatalf("a table was written for the pattern entry:\n%s", text)
	}
}
```

  New `internal/cli/configmigraterepos_test.go`, the verb a user runs:

```go
package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigMigrateConvertsATOMLReposListAndIsThenDone(t *testing.T) {
	e := newMigrateEnv(t)
	writeMigrateFile(t, filepath.Join(e.userDir, "config.toml"),
		"packs = [\"git\", \"github\"]\n\n[allow.github]\nalways = [\"read\"]\non_request = [\"runs\"]\n")
	writeMigrateFile(t, filepath.Join(e.ws, ".git", "config"), "[remote \"origin\"]\n\turl = https://github.com/acme/app.git\n")
	writeMigrateFile(t, filepath.Join(e.ws, ".git", "HEAD"), "ref: refs/heads/main\n")
	project := "[requests.github]\nruns = [\"nightly.yml\"]\n"
	writeMigrateFile(t, filepath.Join(e.ws, "yolo-jail.toml"), project)
	local := filepath.Join(e.ws, "yolo-jail.local.toml")
	writeMigrateFile(t, local, "[requests.github]\nrepos = [\"other/lib\"]\n")
	userBefore := readFile(t, filepath.Join(e.userDir, "config.toml"))

	rc, out, errs := runConfigMigrateVerb(t, "--dry-run")
	if rc != 0 || !strings.Contains(out, "Dry run; nothing was written.") {
		t.Fatalf("dry run rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	for _, want := range []string{"github.com/other/lib", "repos.lib.requests.github.runs", "yolo-jail.local.toml"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the dry run does not print the retained write (%s): %q", want, out)
		}
	}
	if got := readFile(t, local); strings.Contains(got, "[repos") {
		t.Fatalf("a dry run wrote:\n%s", got)
	}

	rc, out, errs = runConfigMigrateVerb(t, "--yes")
	if rc != 0 {
		t.Fatalf("migrate rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	got := readFile(t, local)
	for _, want := range []string{"[repos.lib]", `repo = "github.com/other/lib"`, "[repos.lib.requests.github]", `runs = ["nightly.yml"]`} {
		if !strings.Contains(got, want) {
			t.Fatalf("the local file lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "repos = [") {
		t.Fatalf("the old list survived:\n%s", got)
	}
	if !present(local + ".pre-migrate") {
		t.Fatal("the original was not kept as .pre-migrate")
	}
	if readFile(t, filepath.Join(e.ws, "yolo-jail.toml")) != project || readFile(t, filepath.Join(e.userDir, "config.toml")) != userBefore {
		t.Fatal("the migration wrote a file that held no old spelling")
	}

	rc, out, errs = runConfigMigrateVerb(t, "--yes")
	if rc != 0 || readFile(t, local) != got {
		t.Fatalf("a second run changed something: rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
}
```

  Red: `go test -count=1 -run 'TestMigrate(Turns|Places|Rewrites|Keeps|LeavesALegacyPattern)' ./internal/configmigrate` and
  `go test -count=1 -run TestConfigMigrateConvertsATOMLReposList ./internal/cli`. Predicted:
  `targets = []; want the local file alone`; `plan unequal or incomplete` naming
  `this workspace has more than one remote`; `target` finds none; the dry run prints no retained
  write; `…KeepsTheWritesAnotherRemoteHad`: `the rewritten local file lacks "[repos.upstream.requests.github]"`;
  `TestMigrateLeavesALegacyPatternEntryInTheOldFile`: the entry is converted to `[repos.acme]`, where
  it is not converted.
  `TestMigrateLeavesALegacyWildcardRepositoryGrantInTheOldFile` is a **pin**, and so is the pattern
  test, which turns green with the not-converted branch.
- **Change.** The before side selects through the Task 1 lowering (`currentTask1`, `lowerOld`), and
  its projectscope target-written approximation keeps the user-only and idempotence pins; this task
  refines only the rewrites in the table above. `lower.go` (`lowerBrokered :486`, `lowerGrants`, `grantRequest`): the table above,
  with Task 1's origin lookup in place of `soleRemote` for named-host sources. `graph.go`: mark
  the TOML files. `plan.go`: the in-place rewrite beside `withRemovedSetupKeysConverted`, and one
  transcript line per retained write (identity, file, key) carried on the `Target`.
  The proof's sides are Task 1's: legacy baseline before, normal projection after. Never make it
  pass by changing either projection.
- **Stop** if the proof reports equal while a target file lacks a retained line.
- **Cheap.** The `Target` field that carries the lines (a new one beside `Rewrote`, or
  `Rewrote`); helper names.
- **Gates.** `go test -count=1 ./internal/configmigrate ./internal/config ./internal/cli/...`.
- **Fences.** `internal/configmigrate`, `internal/cli/configmigrate*.go`. No user-file write
  path, no `packs/`, no resolver change.
- **Commit.** `feat(configmigrate): convert repos lists and explicit-repository grants to named tables (GW-D75)`.

### Task 7: refuse the old key (after 6 is green)

- **Settled.** GW-D77: "`requests.<source>.repos` is refused at load naming `[repos.<name>]`
  `repo = "github.com/OWNER/REPO"` and `yolo config migrate`, and `yolo request github repos`
  naming `yolo request repos`." No alias (MT-R3).
- **Tests first.** The rewrites below are the red set; write each before the rows change. One new
  row in `TestRequestReposDeclaresANamedRepository`'s refusal table:
  `{[]string{"yolo", "request", "github", "repos", "acme/other"}, []string{"yolo request repos <alias> github.com/acme/other"}}`.
  In `reposcope_tables_test.go`, `TestTheOldReposKeyIsRefusedNamingItsTableAndTheMigration`: a
  project file `[requests.github]\nrepos = ["other/lib"]\n` through `repoScopeCheck`;
  `check.Fatal` names `requests.github.repos`, `[repos.<name>]`, `github.com/other/lib` and
  `yolo config migrate`, and no source carries a grant. Predicted red: `check.Fatal` is nil.
  `TestConfigMigrateConvertsATOMLReposListAndIsThenDone` must stay green: the file still loads
  for the migration.
- **Change.** The refusal is in the resolver's `repos` arm (`readableallow.go:513`), where the
  declaration and its host are known; the schema rows at `schema.go:255-256, 264-265` stay so the
  file loads for `yolo config migrate` and the edit verbs that remove the key, as a removed setup
  key does. `renames.go:53-54, 56` re-point from `requests.github.repos` to `repos.*.repo`.
  `request.go:107`'s `repos` arm becomes the refusal; `configwhy.go:437`'s arm goes. Not refused:
  legacy `brokered.<S>.repos` in a JSONC file (RC-D19's window); `git`, which keeps its own
  `remote_host` refusal (`readableallow_test.go:75`). No in-jail downgrade is needed: the
  snapshot is JSON, never schema-checked, and `validateBrokeredRequests` returns at once in a
  jail (`brokeredskipped.go:112`). `LegacyWorkspaceBrokerGrantsOf` keeps reading the old key.
- **Rewrites.**
  - `internal/cli/request_test.go:29-32, 50-51`; `jsoncmissingparent_test.go:37` (use
    `github runs nightly.yml`); `configwhy_verb_test.go:17`; `configreftoml_test.go:44`;
    `configeditverbs_test.go:295` (re-key to `requests.github.runs`: it wants any workspace-only
    key and would pass for the wrong reason).
  - `internal/config/schema_test.go:233, 249-252, 529`; `readableconfig_test.go:500`;
    `inherit_test.go:432`; every remaining old-key fixture in the Task 2 list.
  - `internal/cli/run/brokeredscope_requests_test.go:69`, `brokeredscope_gitrequests_test.go:74`,
    `brokeredscope_readable_test.go:26, 66`; `internal/cli/check/brokeredscope_readable_test.go:26, 66`.
  - Hint text `yolo request github repos`: `internal/ghbroker/daemon_test.go:597, 624` with
    `scope.go:137`; `internal/gitbroker/scope_requested_test.go:16` with `broker.go:616`;
    `internal/cli/run/githubbriefing_test.go:44`; `internal/jailcontent/builtinskills_test.go:255`;
    `integration/githubbroker_test.go:706`, and
    `TestGitHubBrokerAWideningEntryAdmitsARepositoryForOneWorkspace`'s fixture.
  - `internal/configmigrate/legacycompleteness_test.go:41` stays `loweredByMigrate`.
  - `TestTheLegacyBaselineFansOutAndTheNewProjectionDoesNot`: its fixture holds the old key, so
    it reads the file with `ReadWorkspaceForGate` alone and drops its gate comparison, which
    `TestTheWorkspaceGrantProjectionAgreesWithTheGate` keeps.
  - **Unchanged on purpose:** every `{"brokered": {"<source>": {"repos": …}}}` fixture in
    `internal/config/brokered_test.go`, `internal/cli/run/brokeredscope_test.go`,
    `liveapply*_test.go`, `check/brokeredaccept_test.go`, `hostconfigkeys_test.go:30`,
    `run/testdata/legacycompat/*`, `internal/loopholedecl/testdata/legacy-shipped/*`. A red there
    means the refusal reached the legacy reader: stop.
- **Text.** The production strings in the Map's Task 7 row.
- **Gates.** `go test -count=1 ./internal/...`; `go vet ./integration`.
- **Fences.** The Map's Task 7 rows. The two broker hint strings are the only lines touched in
  `internal/ghbroker` and `internal/gitbroker`.
- **Commit.** `feat(config)!: refuse requests.<source>.repos naming [repos.<name>] and yolo config migrate (GW-D75)`.

### Task 8: docs and census (after 7)

- **Change.** `userguide/guides/github.md:106-118, 149`; `userguide/reference/cli-reference.md:83`;
  `configuration.md:45`; `settings-per-setup.md:256, 505`; `docs/reference/git-broker.md:174, 412`.
  Each states: unqualified GitHub lines are for `origin`; other repositories are
  `[repos.<name>]`, read-only unless their table asks; the scoped ceiling; a config edit takes
  effect at the next launch or `yolo config apply`. `rg -n 'requests\.github\.repos|github repos' docs/plans/runbooks/mac-actions-runner.md`;
  a live command there is rewritten. Design ledger: Built column for GW-D75, GW-D76, GW-D77,
  GW-D79, GW-D80, and its "Not shipped" status paragraph. This file: `stage: BUILT`.
  Then `python3 scripts/completion-census.py` on the combined tree with the original
  environment, no `TMPDIR` under the workspace; commit its output.
- **Not touched.** `CHANGELOG.md`, `docs/research/*`, other `docs/design/*`.
- **Gates.** `scripts/vantage-check.sh userguide/`, `python3 scripts/check-answered-questions.py`,
  `git diff --check`.
- **Commit.** `docs: repository binding for the github source; census`.

## Verification record

Run on the built series, 2026-11-12. Every command below exited 0 on the commit named.

| What | Commit | Result |
| :--- | :--- | :--- |
| `just check-ci` | `0121997f1` | pass |
| `python3 scripts/completion-census.py` | `0121997f1` | 18273 tests in 178 packages; 1071 reading tests in 43 packages. Its output is committed unedited with this record |
| `go test -count=1 -timeout 0 ./integration` | `e0ad3eaa0` | `ok`, 1590.974s |
| The widening integration tests, focused | `e0ad3eaa0` | pass |
| `TestSelectedHTTPWriteWithdrawalDuringPinnedTLSHandshakeCannotSpend` and `TestPinnedTLSWitnessEndedUnusedOnlyForCloseNotify`, 20 repeats; race, 5 repeats; `go vet`; the whole `internal/callbroker` package | `0121997f1` | pass |
| `TestProductionFloorOperationCancellationReachesActualAdvance`, 20 repeats; race, 5 repeats; `go vet` | `c61e185da` | pass |
| The two per-jail settings fixture tests in `hostservicelifecycle_test.go`, 20 repeats; race, 10 repeats; `go vet` | `600f6048f` | pass |
| `just build-dev-bundle`, then its launcher by absolute path from a throwaway workspace | `8a819c29e` | A launch with a `[repos.lib]` table was accepted; the image built and booted; `/bin/yolo` in the jail reported `8a819c29e933`; the jail shut down normally |

**Which source each result is for.**

- `git diff --name-only e0ad3eaa0 0121997f1` lists four `_test.go` files, under `internal/cli`,
  `internal/cli/run` and `internal/callbroker`. Production source is identical, so the integration
  run stands for `0121997f1`. It was not repeated there.
- `git diff --name-only 8a819c29e 0121997f1` lists those four, `integration/githubbroker_test.go`
  and `userguide/guides/github.md`. The nested launch ran the production code of `0121997f1`.
- `just check-ci` also passed on earlier commits of the series. Those runs are not a substitute
  for the one at `0121997f1` and are not counted.

**Not run, and not claimed.**

- **[Landing](#landing) step 3 by hand.** The nested launch selected no broker and no agent pack.
  Nobody ran `yolo request repos …` or `yolo config migrate --dry-run` by hand in it, and nobody
  read a gate block or a scope file from it. Those surfaces are covered by permanent tests
  instead: `TestRequestReposDeclaresANamedRepository` and its siblings,
  `TestMigrateTurnsATOMLReposListIntoANamedTableWithItsRetainedWrites` and
  `TestRealReadersSeeTheSameGrantsAfterTheTOMLReposMigration`,
  `TestANamedRepositoryIsApprovedAtTheGateAndReachesTheBroker`,
  `TestAStaleApprovalRecordNeverBecomesAScopeFile`, and the GitHub broker tests of the
  integration suite. This record adds no condition; it only says the hand-run step was not done.
- **Native macOS, rootless reachability, live GitHub authority, agent delivery.** None was
  exercised. A nested jail cannot show broker reachability.
- **Live state.** No config file, grant, approval record or broker store was changed.

**Reds during the series.** Each red run was kept and its cause fixed in source before the next
run. Three test-only commits replaced short time budgets with waits on events (`c61e185da`,
`600f6048f`, `db19865f9`), and `c4ce6d8f8` and `0121997f1` made the write-withdrawal test require
a positive TLS witness. The first red of that test was a 5-second wait expiring. Whether machine
load caused it, or a forwarder that unwound only at teardown, was not settled, and no cause is
claimed here.

**Review.** One independent review covered the authority change and one the verbs, migration and
refusal; their findings were repaired in the series, and six lower-priority ones are the first
six items of the [Backlog](#backlog). One independent review of the TLS witness found no blocker.

## Landing

The parent's, once, on the combined branch from a clean worktree:

1. `just check-ci`.
2. `go test -count=1 -timeout 0 ./integration` in full: the launch path changed.
3. `just build-dev-bundle`, then its launcher by absolute path from a throwaway workspace:
   `yolo request repos …`, `yolo config migrate --dry-run`, a launch showing the gate block and
   the scope file. Nothing here is reachability-shaped or rootless-only, and the nested jail is
   not evidence for broker reachability.
4. Proportional review: one reviewer for Tasks 1 to 4 (the authority change), one for Tasks 5
   to 7; Task 8 is read by whoever lands.
5. `just done`.

**After landing, before the owner relaunches.** The outer session's `yolo` predates
`yolo config check` and reads only the old key. The host is updated first (`just install` on the
host, from the landed tree); the frozen outer binary is never the target. The owner's own steps
then follow and are not performed or pre-approved here: drop the dotfiles scope from this
workspace's local file and request `runs = ["*"]` for `mschulkind-oss/yolo-jail`.

## Ships with

- The gate block is the observable surface: each lowered grant with its identity, file and key;
  a retargeted name as one removal and one addition; a skipped request with both allow commands.
- `yolo config migrate`'s transcript and `--dry-run`: one line per retained write.
- `yolo config why repos.…` names the verb; `yolo config-ref` lists the table and the verbs.
- A recorded pattern grant such as `runs github.com/o/* ci.yml` stays inert: it is unmarked, and the
  broker matches only a marked grant. A named pattern table's grant is marked; the gate shows it as
  added, in plain words (every repository under the owner), and asks once. The migration converts
  no pattern entry or pattern grant, and names each one it keeps in the old file.
- `yolo approve`'s listing may show stored entries for a repository now out of scope. They are
  inert, not ended, and cover again if a gate re-admits the repository. No change in this slice.

## Don't

- Don't narrow every named-host source. Only a declaration that opts in narrows.
- Don't leave `WorkspaceBrokerGrantsOf` on the old meaning. The gate and the normal projection
  agree; the old reach is its own function with one caller.
- Don't change `sameRepo`, and don't enumerate a pattern into repositories.
- Don't make the proof pass by switching either projection.
- Don't select targets by repository wildcard, and don't treat `runs = ["*"]` as one.
- Don't branch on a pack name, add an agent or tool-name switch at boot, or add a GitHub-only
  repository key.
- Don't read the approval record for authority, and don't rewrite or expire a stored grant.
- Don't copy a `repo = "*"` grant to the declared names: it is not equal to it.
- Don't match a pattern on an unmarked grant, and don't mark a grant lowered from a legacy entry.
- Don't land, tag or push any task alone. Don't edit this repository's own config files.

## Backlog

Known gaps in the series as built so far, read from the source. None widens what a grant covers,
none blocks landing, and none is fixed in this series. Two terms: the **origin** is the repository
the workspace's `origin` remote names, and a **named table** is a `[repos.<name>]` table.

- **Unsetting a named table's repo line ignores another file's repo line and the user target.**
  `namedRepoLineUnsetRefusal` in [`configeditverbs.go`](../../internal/cli/configeditverbs.go)
  refuses `yolo config unset repos.<name>.repo` when the composed config holds requests under the
  name and the clone has no remote of that name. It does not ask whether another workspace file
  still writes `repos.<name>.repo`, so removing a local file's retarget of a project table is
  refused, and the unset commands it prints for that file remove nothing. It has no branch for a
  user-config target either.
- **The advice for an entry kept in a legacy JSONC file loops.** `notPlainRepoReason` in
  [`reposbinding.go`](../../internal/configmigrate/reposbinding.go) offers
  `yolo request repos <alias> <host>/<path>` for a pattern entry left in a JSONC file's `brokered`
  table. `requestConvertFirst` in [`request.go`](../../internal/cli/request.go) refuses that verb
  on a JSONC file holding `brokered` and names `yolo config migrate`, which keeps the file again.
  The entry has to be removed first.
- **The advice for a retired `requests.<source>.repos` entry stops one step short.** The same
  function, and `noHostReason` beside it, name
  `yolo config unset 'requests.<source>.repos[<n>]'`. The key is still present afterward, and
  [`readableallow.go`](../../internal/config/readableallow.go) refuses the key whenever it is
  present, so `yolo config migrate` has to run again. No test runs that unset on a file holding
  the key, TOML or JSONC.
- **An unqualified list written twice in one file is refused, not merged.** The `place` closure in
  [`lower.go`](../../internal/configmigrate/lower.go) merges two writes of one key only for a
  named table's request line. A legacy grant and a `requests.<source>.<capability>` list that
  lower to the same unqualified key are reported as not converted with "keep one of the two",
  though both are needed. It fails closed.
- **A copied line can be credited to the wrong file.** `place` records the old key as written in
  the file being converted, and `lowerRetainedRequests` then adds the files that really write the
  line. With more than one JSONC file, the transcript of one file can name a line as its own that
  another file wrote. No test converts retained writes across several JSONC files.
- **The named-request unset is TOML only.** `namedRepoUnset` returns early for any other format,
  so `yolo config unset repos.<name>.requests.<source>.<capability>` on a JSONC file takes the
  generic path.

- **The read-withdrawal TLS test still passes on a time budget.**
  `TestSelectedHTTPReadWithdrawalDuringPinnedTLSHandshakeCannotSend` in
  [`http_host_test.go`](../../internal/callbroker/http_host_test.go) bounds both of its waits
  with 5 seconds and has no witness that the TLS handshake succeeded before the refusal. Its
  write counterpart got both in this series. The read test can go red under load, and can pass on
  a failed handshake. Test-only; no production change is implied.

## Blockers

None. No design question is open.
