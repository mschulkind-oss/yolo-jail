package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// The workspace entry at the gate (docs/design/workspace-widening.md §3.2): the config part
// leaves `brokered` out, the scope part takes the entry in, and the sources record makes a
// change of source a row and a prompt. Tested on the gate itself; the launch's call site is
// pinned in internal/cli/run.

// entryScope is one github source whose remotes are remotes and whose entry is entry.
func entryScope(remotes []brokerscope.Remote, entry ...EntryRepo) *ScopeCheck {
	return &ScopeCheck{Sources: []ScopeSource{{Source: "github", Label: "github-broker", RemoteHost: "github.com",
		Read: brokerscope.Read{GitConfig: "/w/.git/config", Remotes: remotes}, Entry: entry}}}
}

// acceptReporter is a ReportPrompter and an AcceptedReporter that records what each was shown.
type acceptReporter struct {
	reportPrompter
	accepted *ChangeReport
}

func (p *acceptReporter) ReportAccepted(r ChangeReport) { p.accepted = &r }

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// WW-D11: `brokered` never moves the config part's bytes, so a record written before this
// change reads the same, and a config holding only the entry records an empty part silently,
// which OQ-S3 allows since nothing in it acts before the scope part's own prompt. A record
// holding `"brokered": null`, which the old validator let through, asks once.
func TestTheConfigPartLeavesBrokeredOut(t *testing.T) {
	ws := approvalWorkspace(t)
	approve(t, ws, decode(t, `{"packages": ["jq"]}`))
	before := readFile(t, ApprovalSnapshotPath(ws))
	with := decode(t, `{"packages": ["jq"], "brokered": {"github": {"repos": ["org/lib"]}}}`)
	ok, err := CheckConfigAndScopeChanges(ws, with, nil, false, false, &failPrompter{t: t})
	if err != nil || !ok {
		t.Fatalf("an entry moved the config part: ok=%v err=%v", ok, err)
	}
	if err := RecordApproval(ws, with, nil); err != nil {
		t.Fatal(err)
	}
	if after := readFile(t, ApprovalSnapshotPath(ws)); after != before {
		t.Fatalf("the config part's bytes moved:\n%s\nwas:\n%s", after, before)
	}

	only := approvalWorkspace(t)
	ok, err = CheckConfigAndScopeChanges(only, decode(t, `{"brokered": {"github": {"repos": ["org/lib"]}}}`),
		nil, false, false, &failPrompter{t: t})
	if err != nil || !ok {
		t.Fatalf("a config holding only the entry asked: ok=%v err=%v", ok, err)
	}
	if got := strings.TrimSpace(readFile(t, ApprovalSnapshotPath(only))); got != "{}" {
		t.Fatalf("its config part is %s, want {}", got)
	}

	old := approvalWorkspace(t)
	write(t, ApprovalSnapshotPath(old), "{\n  \"brokered\": null\n}\n")
	if _, err := CheckConfigAndScopeChanges(old, decode(t, `{"brokered": null}`), nil, false, false, nil); err == nil {
		t.Fatal("a record holding \"brokered\": null passed: it should ask once")
	}
}

// An entry is a row of the scope block naming its file, the header names the files the scope
// was read from, and a count line follows the block; a y records it, and the gate returns what
// it approved, every source labeled.
func TestAnEntryIsARowNamingItsFileAndTheGateReturnsWhatItApproved(t *testing.T) {
	ws := approvalWorkspace(t)
	scope := entryScope([]brokerscope.Remote{origin},
		EntryRepo{Repo: "org/lib", Files: []string{"yolo-jail.jsonc"}},
		EntryRepo{Repo: "O/R", Files: []string{"yolo-jail.local.jsonc"}})
	p := &reportPrompter{accept: true}
	ok, approved, err := ApproveConfigAndScope(ws, decode(t, `{}`), scope, true, false, p)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	block := strings.Join(p.got.ScopeBlock, "\n")
	for _, want := range []string{
		"github-broker repository scope, read from /w/.git/config and yolo-jail.jsonc, yolo-jail.local.jsonc:",
		`+ o/r      remote "origin", yolo-jail.local.jsonc  added`,
		`+ org/lib  yolo-jail.jsonc                         added`,
	} {
		if !strings.Contains(block, want) {
			t.Errorf("block lacks %q:\n%s", want, block)
		}
	}
	if got := strings.Join(p.got.ScopeCounts, "\n"); got != "github-broker: 2 added, 0 removed, 0 source changed" {
		t.Errorf("count line %q", got)
	}
	if got := ApprovedScope(ws, "github"); strings.Join(got, ",") != "o/r,org/lib" {
		t.Errorf("recorded %v", got)
	}
	got := approved["github-broker"]
	if len(got) != 2 || got[0].Repo != "o/r" || strings.Join(got[0].Sources, "|") != `remote "origin"|yolo-jail.local.jsonc` ||
		got[1].Repo != "org/lib" || strings.Join(got[1].Sources, "|") != "yolo-jail.jsonc" {
		t.Errorf("approved %+v", got)
	}
	// Confirmed unchanged, the gate still returns the scope it confirmed.
	ok, approved, err = ApproveConfigAndScope(ws, decode(t, `{}`), scope, false, false, &failPrompter{t: t})
	if err != nil || !ok || len(approved["github-broker"]) != 2 {
		t.Fatalf("a confirmed scope: ok=%v approved=%+v err=%v", ok, approved, err)
	}
}

// WW-D11: a change of source asks, a row of its own: an entry removed while a remote still
// lists the repository, a second source for an approved one, and a remote's rename (OQ-WW1,
// built on its leaning). The decline's next step names the entry's file.
func TestAChangeOfSourceAsks(t *testing.T) {
	ws := approvalWorkspace(t)
	both := entryScope([]brokerscope.Remote{origin}, EntryRepo{Repo: "o/r", Files: []string{"yolo-jail.jsonc"}})
	if err := RecordApproval(ws, decode(t, `{}`), both); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		scope *ScopeCheck
		row   string
		files string
	}{
		"the entry removed under a remote": {githubScope(origin),
			`~ o/r  remote "origin" (was remote "origin", yolo-jail.jsonc)  source changed`, "yolo-jail.jsonc"},
		"a second file for an approved repository": {entryScope([]brokerscope.Remote{origin},
			EntryRepo{Repo: "o/r", Files: []string{"yolo-jail.jsonc", "yolo-jail.local.jsonc"}}),
			`(was remote "origin", yolo-jail.jsonc)  source changed`, "yolo-jail.jsonc,yolo-jail.local.jsonc"},
		"a remote renamed": {entryScope([]brokerscope.Remote{{Name: "upstream", Repo: "o/r"}},
			EntryRepo{Repo: "o/r", Files: []string{"yolo-jail.jsonc"}}),
			`~ o/r  remote "upstream", yolo-jail.jsonc (was remote "origin", yolo-jail.jsonc)  source changed`, "yolo-jail.jsonc"},
	} {
		p := &reportPrompter{accept: false}
		if ok, err := CheckConfigAndScopeChanges(ws, decode(t, `{}`), c.scope, true, false, p); ok || err != nil {
			t.Errorf("%s: ok=%v err=%v, want asked and declined", name, ok, err)
			continue
		}
		if block := strings.Join(p.got.ScopeBlock, "\n"); !strings.Contains(block, c.row) {
			t.Errorf("%s: block lacks %q:\n%s", name, c.row, block)
		}
		if !strings.Contains(strings.Join(p.got.ScopeCounts, "\n"), "1 source changed") {
			t.Errorf("%s: counts %q", name, p.got.ScopeCounts)
		}
		if len(p.got.EntryEdits) != 1 || p.got.EntryEdits[0].Key != "brokered.github.repos" ||
			strings.Join(p.got.EntryEdits[0].Files, ",") != c.files {
			t.Errorf("%s: entry edits %+v, want brokered.github.repos in %s", name, p.got.EntryEdits, c.files)
		}
	}
	// A second remote for an approved repository asks too.
	if err := RecordApproval(ws, decode(t, `{}`), githubScope(origin)); err != nil {
		t.Fatal(err)
	}
	p := &reportPrompter{accept: false}
	CheckConfigAndScopeChanges(ws, decode(t, `{}`), githubScope(origin, brokerscope.Remote{Name: "ssh", Repo: "o/r"}),
		true, false, p)
	if p.got == nil || !strings.Contains(strings.Join(p.got.ScopeBlock, "\n"), `remote "origin", "ssh" (was remote "origin")`) {
		t.Fatalf("a second remote for an approved repository did not ask: %+v", p.got)
	}
}

// The upgrade rule (WW-D11): a scope part with no sources record, as every record written
// before this change is, passes unasked when every source of each approved repository is a
// remote, and the sources are then recorded; a repository an entry lists asks, so deleting the
// sources record alone fails safe.
func TestASourcesRecordMissingFailsSafe(t *testing.T) {
	ws := approvalWorkspace(t)
	if err := RecordApproval(ws, decode(t, `{}`), githubScope(origin)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(ApprovalScopeSourcesPath(ws)); err != nil {
		t.Fatal(err)
	}
	ok, err := CheckConfigAndScopeChanges(ws, decode(t, `{}`), githubScope(origin), false, false, &failPrompter{t: t})
	if err != nil || !ok {
		t.Fatalf("remotes only, no sources record: ok=%v err=%v, want passed unasked", ok, err)
	}
	if got := readFile(t, ApprovalScopeSourcesPath(ws)); !strings.Contains(got, `"remote:origin"`) {
		t.Fatalf("the sources were not recorded: %s", got)
	}

	if err := os.Remove(ApprovalScopeSourcesPath(ws)); err != nil {
		t.Fatal(err)
	}
	p := &reportPrompter{accept: false}
	CheckConfigAndScopeChanges(ws, decode(t, `{}`),
		entryScope([]brokerscope.Remote{origin}, EntryRepo{Repo: "o/r", Files: []string{"yolo-jail.jsonc"}}), true, false, p)
	if p.got == nil || !strings.Contains(strings.Join(p.got.ScopeBlock, "\n"), "(sources not recorded before)  source changed") {
		t.Fatalf("an entry with no sources record passed unasked: %+v", p.got)
	}
}

// WW-D19: a file label the agent chose reaches the block, the count and the advice escaped.
func TestAFileLabelIsEscapedEverywhereTheGatePrintsIt(t *testing.T) {
	ws := approvalWorkspace(t)
	evil := "x\x1b[2K\x1b]0;owned\a.jsonc"
	_, err := CheckConfigAndScopeChanges(ws, decode(t, `{}`),
		entryScope(nil, EntryRepo{Repo: "org/lib", Files: []string{evil}}), false, false, nil)
	e, ok := err.(*ChangedNonInteractiveError)
	if !ok {
		t.Fatalf("err %v", err)
	}
	if strings.ContainsAny(e.Error(), "\x1b\a") {
		t.Fatalf("a terminal sequence from a file name reached the refusal:\n%q", e.Error())
	}
	if !strings.Contains(e.Error(), `x\x1b[2K`) || !strings.Contains(e.Advice(), "(brokered.github.repos)") {
		t.Fatalf("the escaped file is not named:\n%s", e.Error())
	}
}

// WW-D20: with no terminal and --accept-config-changes, the gate shows the block and the count
// lines to the prompter before recording, for remotes as well as entries; and the refusal
// without the flag names the entry's file and key.
func TestTheFlagPathShowsTheBlockBeforeRecording(t *testing.T) {
	ws := approvalWorkspace(t)
	scope := entryScope([]brokerscope.Remote{origin}, EntryRepo{Repo: "org/lib", Files: []string{"yolo-jail.jsonc"}})
	_, err := CheckConfigAndScopeChanges(ws, decode(t, `{}`), scope, false, false, nil)
	e, ok := err.(*ChangedNonInteractiveError)
	if !ok || !strings.Contains(e.Advice(), "scope entry:      "+filepath.Join(ws, "yolo-jail.jsonc")+"  (brokered.github.repos)") {
		t.Fatalf("the refusal does not name the entry's file: %v", err)
	}
	p := &acceptReporter{}
	if ok, err := CheckConfigAndScopeChanges(ws, decode(t, `{}`), scope, false, true, p); err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if p.accepted == nil || !strings.Contains(strings.Join(p.accepted.ScopeBlock, "\n"), "+ org/lib") ||
		len(p.accepted.ScopeCounts) != 1 {
		t.Fatalf("the flag path did not show the block: %+v", p.accepted)
	}
	if got := ApprovedScope(ws, "github"); strings.Join(got, ",") != "o/r,org/lib" {
		t.Fatalf("recorded %v", got)
	}
}

// RecordApproval writes every part; ApprovalRecordFiles names each, so the one path that deletes
// the record (the capture cleanup) deletes them all.
func TestRecordApprovalWritesEveryPartAndTheRecordNamesThem(t *testing.T) {
	ws := approvalWorkspace(t)
	if err := RecordApproval(ws, decode(t, `{}`), entryScope(nil, EntryRepo{Repo: "org/lib", Files: []string{"a.jsonc"}})); err != nil {
		t.Fatal(err)
	}
	files := ApprovalRecordFiles(runtime.FromWorkspace(ws))
	want := []string{ApprovalSnapshotPath(ws), ApprovalScopePath(ws), ApprovalScopeSourcesPath(ws)}
	if strings.Join(files, ",") != strings.Join(want, ",") {
		t.Fatalf("ApprovalRecordFiles %v, want %v", files, want)
	}
	for _, f := range files {
		if !exists(f) {
			t.Errorf("%s not written", f)
		}
	}
	rep, err := ScopeChanges(ws, entryScope(nil, EntryRepo{Repo: "org/lib", Files: []string{"a.jsonc"}},
		EntryRepo{Repo: "org/new", Files: []string{"a.jsonc"}}))
	if err != nil || !rep.ScopeChanged || !strings.Contains(strings.Join(rep.ScopeBlock, "\n"), "+ org/new") {
		t.Fatalf("ScopeChanges %+v err %v", rep, err)
	}
}
