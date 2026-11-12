---
title: "Plan: repository binding for the github source, first slice"
date: 2026-11-12
status: accepted
stage: DECIDED
next: "Build Task 1 (origin-only lowering) and stop at its gates; Tasks 2 to 5 are mapped here and their briefs are still owed"
depends-on:
  - github-write-scopes.md
tags: [plan, github, broker, grants, config]
summary: "Hand-off for GW-D79's first slice. Task 1, origin-only lowering, is complete against the tree with its red test. Tasks 2 to 5 (named tables, the scoped ceiling, the verbs, the migration) are ordered and mapped, and each lists the reading the planner still owes before it is handed over."
---

# Plan: repository binding for the github source, first slice

**Status:** 2026-11-12. Written against `45f576cb4`. **Task 1 is the hand-off.** Tasks 2 to 5 are an
ordered map: each names the seams already read and, under *Owed*, what was not read. Nobody builds
a task whose *Owed* list is not empty.

**Design:** [`github-write-scopes.md`](github-write-scopes.md), [§8](github-write-scopes.md#8-which-repository-a-line-is-for) and
[the first slice](github-write-scopes.md#current-support-and-the-repository-only-first-slice)
([GW-D79](github-write-scopes.md#GW-D79)). The work kinds and the Git tables stay in
[the seed](github-write-scopes-plan.md), which is still a sketch.

**Precedence.** The design wins on behavior. The tree wins on fact, for cosmetic drift only. This
file is advice. **Stop and report, do not commit**, when: the red test does not fail as printed; a
named file is dirty; a named symbol is gone; the fix needs a file outside the task's fence; a gate
fails outside the task's scope.

**Scope.** This workspace's boundary is `mschulkind-oss/yolo-jail` only. No task edits a live
config file, a grant, an approval record, or `yolo-jail.local.toml`.

## Map

| Path | Change | Task |
| :--- | :--- | :--- |
| `internal/config/readableallow.go` | `readableRequestGrants` callers pass origin's target only (`~:730`); missing origin is an error; later, the `repos` root table and scoped ceiling | 1, 2, 3 |
| `internal/config/workspacebrokergrants.go` | the second caller of `readableRequestGrants` (`~:95`), same narrowing | 1 |
| `internal/config/reposcope_origin_test.go` | new, the consumer regression test below | 1 |
| `internal/config/readabledispatch_test.go`, `readableapproverun_test.go` | rewrite to the new behavior | 1 |
| `internal/config/schema.go` (`~:248`) | `repos.*` rows, both scopes | 2 |
| `internal/config/readablescope.go` | gate rows and provenance for `repos.<name>` | 2 |
| `internal/cli/request.go`, `internal/cli/allow.go` | the `repos` and `repos.<name>[.github]` words | 4 |
| `internal/configmigrate/lower.go` (`~:508`), `internal/config/renames.go:53-56`, `removals.go:80-81` | the migration and the refusal | 5 |
| `internal/cli/config_ref.txt`, `userguide/guides/github.md`, `userguide/reference/settings-per-setup.md`, `internal/setupcensus/configkeys.go:539` | text that names `requests.github.repos` | 5 |

## Reuse

- `readableScopeBroker`, `writeReadableScopeRemote`, `writeReadableScopeUser`, `readableScopeEntry`
  in `internal/config/readablescope_test.go`: the fixture for every gate-level test here.
- `NewScopeCheck` / `ReadWorkspaceForGate` (`internal/config/gateread.go`): the consumer surface.
  Test through it, not through `readableRequestGrants`.
- `readableTargets` (`readableallow.go:~1660`) already tags a remote's target `Origin: "remote <name>"`.
- `brokerscope.ReadRemotes(workspace, host)` returns the remotes with their names.
- `outsideCeilingReason` and `ReadableRequestSkipped`: the existing skip-and-report path. An unmet
  ceiling stays a skip.

## Traps

- **Two callers.** `ProduceReadableGateInput` and `WorkspaceBrokerGrantsOf` both fan
  `readableRequestGrants` over `targets`. Changing one makes the gate and the workspace-grant
  projection disagree with no test failing.
- **Pick origin by the remote, not by the label.** `appendReadableRepository` de-duplicates by
  repository, so when two remotes name one repository the surviving `Origin` label is the first
  seen. Resolve the remote named `origin` from `remotes.Remotes` and match its identity.
- **Read scope must not shrink.** Every remote and every listed repository keeps its `read` grant
  and its scope row. Only the non-read request loop narrows.
- **Any-host sources are unchanged.** `policy.RemoteHost == brokerscope.AnyRemoteHost` (git) keeps
  today's every-remote fan-out; the design leaves unqualified `[requests.git]` alone. The branch is
  on the declared `remote_host`, never on a pack or source name.
- **`allow.<source>.always` capabilities** also fan over every target (`readableallow.go:~706`).
  That loop is the user's ceiling acting for every workspace, not a project request. Leave it.
- **Legacy `brokered.<source>.grants`** are placed by `legacyWorkspaceGrant`
  (`readablescope.go:343`), a separate path. Task 1 does not touch it.

## Tasks

### Task 1: origin-only lowering (independent; unblocks the rest)

- **Settled, do not re-open.** "Each unqualified `[requests.github]` line, list or boolean, lowers
  to grants on one concrete identity, the `<host>/<path>` that `origin` resolves to, and on no
  other target." "An unqualified GitHub line where `origin` is absent or not on `github.com`:
  error, not a skip. It names the next step: correct or add an `origin` remote on `github.com`, or,
  if no origin target is meant, move the lines to a `[repos.<name>]` table with its own `repo` and
  qualified requests. An unqualified line never falls back to another repository." "`runs = ["*"]`
  means every workflow within the one identity its table lowers to. It never selects repositories."
- **Preconditions.** `git status --short` empty; `go env GOCACHE` is the shared cache (do not set
  `HOME`, `TMPDIR` or `GOCACHE`); the red command below fails with exactly the two lines shown.
- **Tests first.** New `internal/config/reposcope_origin_test.go`, package `config`:

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
	kinds := map[string]string{"runs": "runs", "approve-run": "approve_runs"}
	broker := readableScopeBroker("github", "github", "github-broker", "github.com", "read", []string{"runs", "approve-run"}, kinds)
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

  Red: `go test -count=1 -run TestUnqualifiedGitHubRequestsLowerToOriginOnly ./internal/config`
  prints `an unqualified request reached a repository other than origin: runs github.com/elsewhere/app *`
  and the same for `approve-run github.com/elsewhere/app github-actions[bot]` (confirmed at
  `45f576cb4`). Add two more cases in the same file, same fixture: a workspace whose only remote is
  `upstream` and one whose `origin` is on another host; each expects `check.Fatal` non-nil, its text
  naming `origin` and `[repos.<name>]`, and no grant other than `read`.
- **Change.** One helper in `readableallow.go` returning the origin target for a named-host policy
  (or an error carrying the next step); both callers hand `readableRequestGrants` that single
  target. The error surfaces through `readableScopeFailure`, as other resolver errors do. The
  helper's name and signature are cheap and yours.
- **Tests that break and are rewritten, not repaired.** `TestReadableDispatchWorkflowsRequestBecomesAGrantPerTarget`
  (`readabledispatch_test.go:22`) and the matching case in `readableapproverun_test.go:26` assert a
  grant on a `requests.github.repos` entry with no `origin`. Rewrite each to an `origin` fixture
  asserting the grant on origin and none on the listed repository.
- **Gates.** `go test -count=1 ./internal/config ./internal/cli/... ./internal/configmigrate ./internal/ghbroker`,
  then `just format`. The parent runs `just check-ci`, the nested run and
  `go test -run 'GitHubBroker' ./integration`.
- **Fences.** Nothing under `internal/ghbroker`, `internal/brokerstore`, `packs/`, `docs/`; no
  schema row; no verb. Tier 3: this fence is a request, nothing enforces it.
- **Commit.** `feat(config): lower unqualified named-host requests to origin only (GW-D79)`.
- **Report.** The red output, the green gates, and every other test that failed with its old
  assertion quoted. A failing test outside `internal/config` is a stop, not a rewrite.

### Tasks 2 to 5: mapped, not handed over

| Task | Order | Read at `45f576cb4` | Owed before hand-off |
| :--- | :--- | :--- | :--- |
| 2. `[repos.<name>]` tables: schema, validation, read-only target, `[repos.<name>.requests.github]` lowering, gate rows, retarget asks again | after 1 | schema rows `schema.go:238-262`; the `repos` branch `readableallow.go:513`; `compareScope` (`scopeapproval.go:493`) | how merge layers a root table with a scalar `repo`; where origins for a `repos.*` path are produced (`readableWorkspaceOrigins`); the gate's identity-diff rendering; the red test |
| 3. `[repos.<name>.allow.github]` scoped ceiling, additive, matched by identity | after 2 | the `allow` loop `readableallow.go:~230-330`; `outsideCeilingReason` | wildcard `repo` matching in user scope; `UserBrokerAuthorityOf` consumers; the red test |
| 4. Verbs: `yolo request repos`, `repos.<name>.github`; `yolo allow repos`, `repos.<name>.github`; help, `config-ref` | after 2 and 3 | `requestRunIn` (`request.go:56`), its `repos` branch (`:107`) | `internal/cli/allow.go`; the dispatch-registry test `TestConfigRefTOMLSectionNamesTheVerbsTheCLIHas`; `config why` (`configwhy.go:437`) |
| 5. Migration of `requests.github.repos` and legacy grants, then the refusal | after 4 | `lower.go:508`, `renames.go:53-56`, `removals.go:80-81` | whether `yolo config migrate` rewrites a file already in TOML; the RC-D55 proof's inputs; the legacy `"*"` case, which the design does not claim equal |

**Landing order is a constraint.** Task 1 alone narrows what a `requests.github.repos` entry
receives, with no spelling yet to ask for it back. Tasks 1 to 5 land on `main` as one series, with
no release between them.

## Ships with

- **Stored approvals (design's "Unverified").** Read at `45f576cb4`: `compareScope` writes
  `<source>.grants` from the current check only (`scopeapproval.go:~544`), and a launch hands its
  broker the gate's result, so a grant no longer lowered leaves the record at the next approval.
  Not read: whether a running broker's scope file drops it before relaunch, and whether a
  `brokerstore` grant or working-set entry outlives it. Task 2 owes a test for each.
- Integration: `integration/githubbroker_test.go` names the old key; its rewrite belongs to Task 5.
- `python3 scripts/completion-census.py`, complete, on the combined tree, since tests here read
  files outside their package. No `TMPDIR` under the workspace.
- Docs and text naming the old spelling: listed in the map under Task 5.

## Don't

- Don't select targets by repository wildcard, and don't treat `runs = ["*"]` as one.
- Don't add a GitHub-only repository key, or branch on the pack name `github`.
- Don't refuse `requests.github.repos` before Task 5's migration exists.
- Don't edit this repository's `yolo-jail.local.toml`; today's yolo reads only the old spelling.

## Blockers

None for Task 1. No design question is open.
