package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
)

func auditFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "broker", "audit.jsonl")
	l := brokeraudit.Open(path, nil)
	zero, seventy := 0, 77
	for _, e := range []brokeraudit.Event{
		{Time: "2026-09-29T10:00:00Z", Event: "call", Service: "github", Jail: "aaa", Workspace: "/w/one",
			Argv: []string{"pr", "view", "--repo=o/r", "32"}, Set: "read-only", Outcome: "ran", Exit: &zero},
		{Time: "2026-09-29T12:00:00Z", Event: "call", Service: "github", Jail: "bbb", Workspace: "/w/two",
			Argv: []string{"auth", "token"}, Set: "refused", Outcome: "refused", Exit: &seventy},
		{Time: "2026-09-29T13:30:00Z", Event: "call", Service: "github", Jail: "aaa", Workspace: "/w/one",
			Argv: []string{"pr", "comment", "--repo=o/r", "1"}, Set: "read-write", Outcome: "denied", Exit: &seventy},
	} {
		l.Append(e)
	}
	return path
}

func runAuditCase(t *testing.T, path string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	now := time.Date(2026, 9, 29, 14, 0, 0, 0, time.UTC)
	rc := auditMain(args, false, path, now, &out, &errOut)
	return rc, out.String(), errOut.String()
}

func TestAuditListsEveryCallOldestFirst(t *testing.T) {
	path := auditFixture(t)
	rc, out, _ := runAuditCase(t, path)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if rc != 0 || len(lines) != 3 {
		t.Fatalf("rc %d out %q", rc, out)
	}
	if !strings.Contains(lines[0], "read-only") || !strings.Contains(lines[0], "github: pr view --repo=o/r 32") ||
		!strings.Contains(lines[0], "[/w/one]") {
		t.Fatalf("first line %q", lines[0])
	}
}

func TestAuditFilters(t *testing.T) {
	path := auditFixture(t)
	for _, c := range []struct {
		args []string
		want int
	}{
		{[]string{"--jail", "aaa"}, 2},
		{[]string{"--workspace=/w/two"}, 1},
		{[]string{"--set", "refused"}, 1},
		{[]string{"--since", "1h"}, 1},
		{[]string{"--since", "3h"}, 2},
		{[]string{"--since", "1d"}, 3},
		{[]string{"--since", "2026-09-29T11:00:00Z"}, 2},
		{[]string{"--grant", "g-1"}, 0},
	} {
		_, out, _ := runAuditCase(t, path, append(c.args, "--json")...)
		got := 0
		if s := strings.TrimSpace(out); s != "" {
			got = len(strings.Split(s, "\n"))
		}
		if got != c.want {
			t.Errorf("yolo audit %v: %d lines, want %d:\n%s", c.args, got, c.want, out)
		}
	}
}

func TestAuditRefusesInAJail(t *testing.T) {
	var out, errOut bytes.Buffer
	rc := auditMain(nil, true, auditFixture(t), time.Now(), &out, &errOut)
	if rc != 2 || !strings.Contains(errOut.String(), "host-only") || out.Len() != 0 {
		t.Fatalf("rc %d out %q err %q", rc, out.String(), errOut.String())
	}
}

func TestAuditRefusesAnUnknownArgument(t *testing.T) {
	rc, _, errOut := runAuditCase(t, auditFixture(t), "--frob")
	if rc != 2 || !strings.Contains(errOut, "--frob") {
		t.Fatalf("rc %d err %q", rc, errOut)
	}
	if rc, _, errOut := runAuditCase(t, auditFixture(t), "--since", "yesterday"); rc != 2 || !strings.Contains(errOut, "duration") {
		t.Fatalf("rc %d err %q", rc, errOut)
	}
}

// Through the registry, the way `yolo audit` is dispatched: the handler is handed the verb
// first. Measured as a real defect by the integration suite before this test existed.
func TestAuditThroughTheRegistry(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	l := brokeraudit.Open(filepath.Join(home, ".local", "share", "yolo-jail", "broker", "audit.jsonl"), nil)
	zero := 0
	l.Append(brokeraudit.Event{Event: "call", Service: "github", Jail: "j", Argv: []string{"pr", "list"},
		Set: "read-only", Outcome: "ran", Exit: &zero})
	rc, out, errOut := runMainCaptured(t, "audit", "--json", "--set", "read-only")
	if rc != 0 || !strings.Contains(out, `"argv":["pr","list"]`) {
		t.Fatalf("rc %d out %q err %q", rc, out, errOut)
	}
}

func TestAuditWithNoLog(t *testing.T) {
	rc, out, _ := runAuditCase(t, filepath.Join(t.TempDir(), "none.jsonl"))
	if rc != 0 || !strings.Contains(out, "No brokered calls recorded") {
		t.Fatalf("rc %d out %q", rc, out)
	}
}
