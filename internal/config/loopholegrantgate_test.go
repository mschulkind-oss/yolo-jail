package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// grantFixture is one grant-declaring loophole, shaped like the journal: `read` requestable by
// default, `read_all` not.
func grantFixture() []GateLoophole {
	return []GateLoophole{{Pack: "fixture", Loophole: "fixture", Access: "read the host's journal",
		Grants: []loopholedecl.Grant{
			{Kind: "read", Text: "read your user journal", RequestableByDefault: true},
			{Kind: "read_all", Text: "read the whole host journal"},
		}}}
}

// writeGrantUserConfig writes the user's TOML config under HOME.
func writeGrantUserConfig(t *testing.T, body string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, paths.UserConfigTOMLName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// grantGate runs one fresh launch's gate over the workspace as it is now, through the gate's own
// read and CheckConfigAndScopeChanges, answering y; it returns the report the human was shown,
// nil when the gate asked nothing.
func grantGate(t *testing.T, ws string, lps []GateLoophole) *ChangeReport {
	t.Helper()
	read, err := ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	check := WithLoopholeGrants(nil, ws, read, lps)
	if check != nil && check.Fatal != nil {
		t.Fatalf("grant check: %v", check.Fatal)
	}
	p := &reportPrompter{accept: true}
	ok, err := CheckConfigAndScopeChanges(ws, read.Config, check, true, false, p)
	if err != nil || !ok {
		t.Fatalf("gate ok=%v err=%v", ok, err)
	}
	return p.got
}

// An `always` grant in the user's ceiling shows at each project's gate once, is recorded by the
// y, and asks nothing again until the ceiling changes; withdrawn, it shows removed once
// (docs/reference/loophole-grants.md LG-D12).
func TestAnAlwaysGrantShowsOnceAtEachProjectsGate(t *testing.T) {
	ws := approvalWorkspace(t)
	writeGrantUserConfig(t, "[allow.fixture]\nalways = [\"read\"]\n")

	first := grantGate(t, ws, grantFixture())
	if first == nil {
		t.Fatal("the first gate asked nothing about the always grant")
	}
	want := "  + fixture: read your user journal (from your user config, for every project)"
	if !first.GrantsChanged || !containsLine(first.GrantBlock, want) {
		t.Fatalf("first gate's grant block %q, want the line %q", first.GrantBlock, want)
	}
	if first.ConfigChanged || first.ScopeChanged {
		t.Fatalf("an empty workspace config and no broker reported a config or scope change: %+v", first)
	}

	if again := grantGate(t, ws, grantFixture()); again != nil {
		t.Fatalf("the second gate asked again about an approved grant: %+v", again)
	}

	writeGrantUserConfig(t, "packs = []\n")
	removed := grantGate(t, ws, grantFixture())
	wantRemoved := "  - fixture: read your user journal (removed)"
	if removed == nil || !containsLine(removed.GrantBlock, wantRemoved) {
		t.Fatalf("a withdrawn ceiling did not show the grant removed: %+v", removed)
	}
	if again := grantGate(t, ws, grantFixture()); again != nil {
		t.Fatalf("the removal was shown twice: %+v", again)
	}
}

// The record's grant part is a part like the others: ApprovalRecordFiles names it, so the one
// path that deletes the record deletes it too, and an approval writes `{"<pack>": ["<kind>"]}`.
func TestTheGrantPartJoinsTheApprovalRecord(t *testing.T) {
	ws := approvalWorkspace(t)
	writeGrantUserConfig(t, "[allow.fixture]\nalways = [\"read\"]\n")
	part := ApprovalLoopholeGrantsPath(ws)
	if !strings.HasSuffix(part, runtime.FromWorkspace(ws)+".loophole-grants.json") {
		t.Fatalf("grant part at %s", part)
	}
	found := false
	for _, f := range ApprovalRecordFiles(runtime.FromWorkspace(ws)) {
		found = found || f == part
	}
	if !found {
		t.Fatalf("ApprovalRecordFiles does not name the grant part %s", part)
	}
	if grantGate(t, ws, grantFixture()) == nil {
		t.Fatal("the gate asked nothing")
	}
	data, err := os.ReadFile(part)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(strings.Fields(string(data)), ""), `{"fixture":["read"]}`; got != want {
		t.Fatalf("grant part %s, want %s", data, want)
	}
}

// A request within the ceiling is a line naming where it was written; a request outside it is a
// skipped line naming the command that allows it, shown because the gate asks anyway; a valued
// kind is one line per value. A declined gate records nothing.
func TestARequestedGrantNamesItsLineAndASkippedOneItsCommand(t *testing.T) {
	ws := approvalWorkspace(t)
	writeGrantUserConfig(t, "[allow.procs]\non_request = [\"view\"]\n")
	if err := os.WriteFile(filepath.Join(ws, WorkspaceTOMLConfigName),
		[]byte("[requests.procs]\nview = [\"postgres\", \"redis-server\"]\n\n[requests.fixture]\nread_all = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lps := append(grantFixture(), GateLoophole{Pack: "procs", Loophole: "procs", Grants: []loopholedecl.Grant{
		{Kind: "view", Text: "see", Valued: true, ValueNoun: "process names"}}})
	read, err := ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	check := WithLoopholeGrants(nil, ws, read, lps)
	p := &reportPrompter{accept: false}
	if ok, err := CheckConfigAndScopeChanges(ws, read.Config, check, true, false, p); ok || err != nil {
		t.Fatalf("declined gate ok=%v err=%v", ok, err)
	}
	for _, want := range []string{
		"  + procs: see postgres (requested by " + WorkspaceTOMLConfigName + ":2)",
		"  + procs: see redis-server (requested by " + WorkspaceTOMLConfigName + ":2)",
		"  – fixture: read_all (not allowed on request by your config: yolo allow fixture on_request read_all)",
	} {
		if p.got == nil || !containsLine(p.got.GrantBlock, want) {
			t.Fatalf("grant block lacks %q:\n%s", want, strings.Join(p.got.GrantBlock, "\n"))
		}
	}
	if exists(ApprovalLoopholeGrantsPath(ws)) {
		t.Fatal("a declined gate recorded the grant part")
	}
	standings := check.LoopholeGrantStandings(ws)
	if got := standings["procs"]["view"]; got.Standing != LoopholeGrantPending || len(got.Values) != 2 {
		t.Fatalf("requested, in the ceiling and not recorded is pending; got %+v", got)
	}
	if got := standings["fixture"]["read_all"]; got.Standing != LoopholeGrantNotAllowed {
		t.Fatalf("read_all outside the ceiling reads %+v", got)
	}
	if got := standings["fixture"]["read"]; got.Standing != LoopholeGrantRequestable {
		t.Fatalf("read, requestable by default and not asked for, reads %+v", got)
	}
}

// An inert loophole's request stays out of the gate (LG-D10): no line, no record, and it is kept
// apart so `yolo check` can name it inert.
func TestAnInertLoopholesGrantStaysOutOfTheGate(t *testing.T) {
	ws := approvalWorkspace(t)
	writeGrantUserConfig(t, "[allow.fixture]\nalways = [\"read\"]\n")
	lps := grantFixture()
	lps[0].Inert = "loophole fixture is not started on this backend"
	if got := grantGate(t, ws, lps); got != nil {
		t.Fatalf("an inert loophole's grant reached the gate: %+v", got)
	}
	if exists(ApprovalLoopholeGrantsPath(ws)) {
		t.Fatal("an inert loophole's grant was recorded")
	}
}

func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}
