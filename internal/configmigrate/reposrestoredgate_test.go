package configmigrate

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// gateCheckOf reads the workspace through the launch's gate reader and builds the github scope
// check the launch builds from the shipped declaration.
func gateCheckOf(t *testing.T, ws string) *config.ScopeCheck {
	t.Helper()
	decls, _, err := config.ReadableDeclarationsForPacks(packload.Embedded())
	if err != nil {
		t.Fatalf("shipped declarations: %v", err)
	}
	read, err := config.ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatalf("the workspace does not read through the gate: %v", err)
	}
	check := config.NewScopeCheck(ws, []config.ScopeBroker{shippedGitHubScopeBroker(t, decls)}, read)
	if check == nil || check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: %+v", check)
	}
	return check
}

// A narrowed record approved before a legacy repos list is migrated: the migration restores the
// list's writes as the gate's ask, never as approval. The approved record keeps the narrow grants
// until the owner accepts the migrated scope.
func TestMigratedRetainedWritesAskAfterTheNarrowedScopeWasApproved(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.toml"), reposUserTOML)
	ws := realDir(t)
	namedRemotes(t, ws, "origin", "https://github.com/acme/app.git")
	write(t, filepath.Join(ws, oracleProject), "[requests.github]\nruns = [\"nightly.yml\"]\n")

	// The narrowed record: origin's writes only, approved as the gate asks them.
	if err := config.RecordApproval(ws, nil, gateCheckOf(t, ws)); err != nil {
		t.Fatal(err)
	}
	const otherWrite = "runs github.com/other/lib nightly.yml"
	if got := config.ApprovedGrants(ws, "github"); containsString(grantStringsOf(got), otherWrite) {
		t.Fatalf("the narrowed record already holds %s: %v", otherWrite, got)
	}

	// The legacy local list arrives: the gate asks for it before the migration runs.
	localPath := filepath.Join(ws, oracleLocal)
	write(t, localPath, "[requests.github]\nrepos = [\"other/lib\"]\n")
	before, err := config.ScopeChanges(ws, gateCheckOf(t, ws))
	if err != nil {
		t.Fatal(err)
	}
	if !before.ScopeChanged {
		t.Fatalf("the gate does not ask for the legacy list's writes before the migration")
	}

	// THE MIGRATION does not approve anything.
	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if !p.Equal || len(p.NotConverted) > 0 {
		t.Fatalf("plan unequal or incomplete: %+v %+v", p.Differences, p.NotConverted)
	}
	if _, err := Apply(p, ApplyOptions{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := grantStringsOf(config.ApprovedGrants(ws, "github")); containsString(got, otherWrite) {
		t.Fatalf("the migration approved %s: %v", otherWrite, got)
	}

	// After the migration the gate asks for the same writes, with the same added line.
	after, err := config.ScopeChanges(ws, gateCheckOf(t, ws))
	if err != nil {
		t.Fatal(err)
	}
	if !after.ScopeChanged {
		t.Fatalf("the migrated scope does not ask: the migration silently granted %s", otherWrite)
	}
	if !containsString(after.ScopeBlock, "+ grant runs github.com/other/lib nightly.yml") {
		t.Fatalf("the gate's block does not show the restored writes as added:\n%s", strings.Join(after.ScopeBlock, "\n"))
	}

	// The owner accepts the migrated scope: the record now holds the restored writes, and the gate is quiet.
	if err := config.RecordApproval(ws, nil, gateCheckOf(t, ws)); err != nil {
		t.Fatal(err)
	}
	if got := grantStringsOf(config.ApprovedGrants(ws, "github")); !containsString(got, otherWrite) {
		t.Fatalf("the accepted record holds %v, want %s", got, otherWrite)
	}
	if rep, err := config.ScopeChanges(ws, gateCheckOf(t, ws)); err != nil || rep.ScopeChanged {
		t.Fatalf("the accepted migrated scope asks again: %v\n%s", err, strings.Join(rep.ScopeBlock, "\n"))
	}
}

func grantStringsOf(grants []brokerscope.Grant) []string {
	out := make([]string, 0, len(grants))
	for _, g := range grants {
		out = append(out, g.String())
	}
	return out
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}
