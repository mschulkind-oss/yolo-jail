package ghbroker

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
)

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

// rawScopeGrants rewrites the bound scope file's grants without ReplaceScopeContext, which
// refuses a grant ValidGrant refuses. The broker reads the file as it is, so this is the only way
// a broker meets a marked grant its gate did not approve.
func rawScopeGrants(t *testing.T, b *Broker, grants []brokerscope.Grant) {
	t.Helper()
	f, err := brokerscope.ReadFile(b.authority.path)
	if err != nil {
		t.Fatal(err)
	}
	f.Grants = grants
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b.authority.path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A grant an older build's gate recorded on a pattern matched no command, so it stays inert in a
// broker that matches patterns; only a grant the gate approved as a pattern covers, behind the
// read floor and the live ceiling (GW-D80). A marked grant that ValidGrant refuses covers nothing,
// and an unmarked `*` keeps its legacy meaning.
func TestOnlyAGrantApprovedAsAPatternCoversByPattern(t *testing.T) {
	const (
		legacyRuns    = `{"kind":"runs","repo":"github.com/o/*","workflow":"*"}`
		patternRuns   = `{"kind":"runs","repo":"github.com/o/*","workflow":"*","repo_pattern":true}`
		otherRuns     = `{"kind":"runs","repo":"github.com/elsewhere/*","workflow":"*","repo_pattern":true}`
		readAny       = `{"kind":"read","repo":"*"}`
		legacyRead    = `{"kind":"read","repo":"github.com/o/*"}`
		patternRead   = `{"kind":"read","repo":"github.com/o/*","repo_pattern":true}`
		legacyAnyRuns = `{"kind":"runs","repo":"*","workflow":"*"}`
		markedAnyRuns = `{"kind":"runs","repo":"*","workflow":"*","repo_pattern":true}`
		markedExact   = `{"kind":"runs","repo":"github.com/o/r","workflow":"*","repo_pattern":true}`
		markedPartial = `{"kind":"runs","repo":"github.com/o*/*","workflow":"*","repo_pattern":true}`
		otherHost     = `{"kind":"runs","repo":"example.com/o/*","workflow":"*","repo_pattern":true}`
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
		{"a legacy unmarked wildcard repository still covers", "[" + readAny + "," + legacyAnyRuns + "]", "'runs'", true},
		{"a marked wildcard repository is not the legacy wildcard", "[" + readAny + "," + markedAnyRuns + "]", "'runs'", false},
		{"a marked exact repository is refused", "[" + readAny + "," + markedExact + "]", "'runs'", false},
		{"a marked partial segment is refused", "[" + readAny + "," + markedPartial + "]", "'runs'", false},
		{"a marked pattern on another host", "[" + readAny + "," + otherHost + "]", "'runs'", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, gh := runsBroker(t)
			grantRuns(t, b, c.onRequest) // the user config; the scope file is replaced below
			if err := brokerscope.ReplaceScopeContext(context.Background(), b.authority.path, b.authority.scope,
				[]string{"o/*"}, nil); err != nil {
				t.Fatal(err)
			}
			rawScopeGrants(t, b, scopeFileGrants(t, c.grants))
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

// An approve-run grant marked as a pattern approves a covered run by pattern, and a pattern on
// another owner covers nothing.
func TestOnlyAPatternApproveRunGrantCoversByPattern(t *testing.T) {
	for _, c := range []struct {
		name    string
		grant   brokerscope.Grant
		covered bool
	}{
		{"a pattern on the repository's owner", brokerscope.Grant{Kind: GrantApproveRun, Repo: "github.com/o/*", Actor: brokerscope.AnyActor, RepoPattern: true}, true},
		{"a pattern on another owner", brokerscope.Grant{Kind: GrantApproveRun, Repo: "github.com/elsewhere/*", Actor: brokerscope.AnyActor, RepoPattern: true}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, gh := approveBroker(t, runJSON("12345", "octocat", "o/r"))
			grantApprove(t, b, true, c.grant)
			code, msg := serveGH(b, approveArgv...)
			if c.covered && (code != 0 || len(gh.writes()) != 1) {
				t.Fatalf("not covered: code %d, writes %v: %s", code, gh.writes(), msg)
			}
			if !c.covered && (code == 0 || len(gh.writes()) != 0) {
				t.Fatalf("covered: code %d, writes %v: %s", code, gh.writes(), msg)
			}
		})
	}
}
