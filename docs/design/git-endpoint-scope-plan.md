---
status: accepted
stage: DECIDED
next: "Implement item18 RED first in locked durable candidate tree"
depends-on: []
---
# Item18 — narrow SSH endpoint scope repair

Written against 662d9103b677594de38e6f96f7a07ec693fbf28a, 2026-10-29. Owner's item18 ruling supersedes exact-string-only matching in [ID-2](../reference/git-broker.md#ID-2). Behavior ruling → actual tree → this plan. This is a candidate handoff, not Main acceptance.

## Settled (do not reopen)
One approved `git@github.com:mschulkind/dotfiles` permits requests `ssh://git@github.com/mschulkind/dotfiles` and `git+ssh://git@github.com/mschulkind/dotfiles`. Keep original request URL in remote helper, upstream argv path, and audit. No grant/remotes/approval changes. Never use gitproto.Identity as endpoint approval: it drops username/transport and collapses paths.
SSH scheme aliases (`ssh`, `git+ssh`, `ssh+git`) may match when existing parseSSH yields identical destination, explicit port and effective path. Destination and port compare exactly; no host aliases, default-port collapsing or username dropping. Generic scp relative versus URL absolute stays distinct. Only exact `git@github.com`, no port, ordinary two-segment owner/repo paths get known public GitHub relative/absolute addressing equivalence. Do not strip .git, trailing slash, case, ~/ or encoding. HTTPS remains exact literal matching and never equivalent to SSH. Preserve exact-match behavior for existing callers; equivalent matching refuses ambiguous encoding/helper/traversal forms. No external network/credentials/API/push/real agent.

## Map / fence (all other files forbidden)
| Path | Change |
| :--- | :--- |
| internal/gitbroker/scope_endpoint.go (new) | source-specific scopeContains and narrow SSH comparison, reusing parseSSH |
| internal/gitbroker/scope_endpoint_test.go (new) | permanent real-Git RED and endpoint denies/fresh revocation |
| internal/gitbroker/broker.go | only three slices.Contains checks and three ValidateCurrent calls; remove slices import if unused |
| internal/brokerscope/file.go | Preserve ValidateCurrent; optional explicitly supplied remote matcher variant shares SAME freshness/identity/corruption checks |
| internal/brokerscope/remote_match_test.go (new) | default other-source matching remains exact; freshness unchanged |
| docs/design/git-broker.md | scope paragraphs only, precise narrow equivalence (since graduated into the reference) |
| docs/design/git-broker-plan.md | ID-2 matching statement only, original URL preserved (ID-2 now lives in the reference) |
| docs/research/git-broker-url-coverage.md | boundary/table precise current limits, preserve historical RED/QA anatomy |
| internal/completiondata/** (only generated census output if changed) | completion-census generation only |

No gitproto, remote helper, pack, upstream parser, push/policy/approval code changes; existing broker_test.go and sibling19 files read-only. Fences tier3 named scope checked by lead/reviewer diff. Stop/report missing symbols, dirty files, mismatch RED, outside-fence need, out-of-scope gate failure. No reset/amend/rebase/stash/cache/global-config mutation.

## Reuse / consumers / traps
- gitbroker.parseSSH/splitSSH and transportOf in upstream.go already refuse unsupported schemes, option injection, decoded URL differences and validate Identity path segments. Reuse parser, do not create divergent parser. Reject percent/ambiguous syntax when comparing nonliteral spellings; exact matching retains old policy.
- fixture/newFixtureOpts, sshScript, writeExec, f.git, f.setScope, auditEvents, writeUserConfig from broker_test.go; f.git loads actual packs/git/pack.json rewrites and caller-side fail-closed SSH command. Helpers run real Git against bare repository and fake loopback TLS.
- broker.go Handle checks initial membership, non-HTTPS ValidateCurrent, credentialPolicy fresh membership/current validation, HTTPS in-scope anonymous current validation. ALL use one source-specific comparator; HTTPS literal behavior unchanged.
- brokerscope.ValidateCurrent in file.go additionally rereads source, launch, workspace, container, PID, validates entries/form and includes Widened. Preserve these and existing other brokers' default admissions. Explicit callback from Git only rather than source-name-dependent blanket policy. Keep nil/default exact behavior. Do not bypass through a stale preselected matching string.
- Existing TestSameIdentityDoesNotApproveAnotherSSHURL approves relative scp at fixture.test but requests absolute URL: MUST remain denied. TestValidateCurrentRemoteURLForm's missing .git remains denied.
- go env original HOME: GOCACHE=/home/agent/.cache/go-build GOMODCACHE=/home/agent/go/pkg/mod GOENV=/home/agent/.config/go/env. Keep Go HOME unchanged; only runtime CLI smoke receives temporary empty config/HOME. Do not mutate caches.

## Tests first — permanent caller regression (exact seed)
Add this test FIRST without production changes, run RED and preserve receipt, then repair. Cheap implementation choice: test factoring/names beyond this seed.
```go
func TestApprovedGitHubScopeAcceptsSSHSpellings(t *testing.T) {
 const approved = "git@github.com:mschulkind/dotfiles"
 for _, remote := range []string{"ssh://git@github.com/mschulkind/dotfiles", "git+ssh://git@github.com/mschulkind/dotfiles"} {
  t.Run(remote, func(t *testing.T) {
   f := newFixture(t, "", approved)
   body := "case \"$last\" in \"git-upload-pack '/mschulkind/dotfiles'\"|\"git-upload-pack 'mschulkind/dotfiles'\") ;; *) echo 'unexpected upstream' >&2; exit 97;; esac\nexec git-upload-pack '" + f.bare + "'"
   writeExec(t, filepath.Join(f.bin, "ssh"), sshScript(f.record, filepath.Join(f.root, "srv"), body))
   out, err := f.git("ls-remote", remote)
   if err != nil || !strings.Contains(out, "\trefs/heads/main") { t.Fatalf("approved GitHub alternate %q: %v\n%s", remote, err, out) }
   events := auditEvents(t)
   if len(events) != 1 || events[0].Set != "read" || events[0].Remote != remote { t.Fatalf("original URL not retained: %+v", events) }
  })
 }
}
```
RED command: `go test ./internal/gitbroker -run '^TestApprovedGitHubScopeAcceptsSSHSpellings$' -count=1 -timeout=60s`; expected `remote not in approved scope`, no fake SSH. Save RED under /workspace/.yolo/durable/git-scope-force-20261029/issue1-red.log; never overwrite earlier receipts.
Then add real-Git fetch/clone transfer through same helper fixture and guarded fake SSH; one approved spelling only, audit originalURL, assert upstream argv destination/path intact. Cross-user/port/host/SSHalias/path/repo/HTTPS/encoding/traversal/helper golden negatives; real-Git routed denies no SSH dispatch/no credential-helper invocation/no pending approval/request. Fresh setScope revocation and live read-grant removal between requests must deny with no new upstream effect. Exercise generic same-path scheme aliases, generic relative-vs-absolute deny; pure comparator tests supplement caller tests, not replace them. Reuse HTTPS fixture tests for fresh credential/read/scope decisions; no TLS relaxation.

## Gates / commit / report
- After GREEN: `just format`; `python3 scripts/completion-census.py` since fixture packGitRewriteEnv reads manifest outside package. Locate actual generated output; if outside listed census directory report before editing.
- `go test -short ./internal/gitbroker ./internal/brokerscope ./internal/gitproto ./internal/gitremote ./internal/ghbroker -count=1 -timeout=180s`
- `go test -race ./internal/gitbroker ./internal/brokerscope -count=1 -timeout=240s`
- `just build-dev-bundle`, emitted absolute launcher from throwaway nonrepo workspace with safe empty packs config, no API/install/agent; matched scoped `go test -v -run '^TestGitBrokerFetchAndRefusal$' ./integration -count=1 -timeout=0` (bounded process budget). No full suite/check-ci per lane; Root owns combined gate.
- One implementation commit: `fix(git-broker): match narrowly equivalent SSH scope endpoints` (RED receipt preserved before it). Retain locked tree; commit and report candidate SHA/ref/tree, exact diffstat/fence, RED/GREEN receipts, test gaps and no Main claim. Do not run just done if it triggers whole-tree gate; report Root owes completion.
