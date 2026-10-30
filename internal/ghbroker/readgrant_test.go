package ghbroker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// The broker of a selected pack grants nothing (docs/design/boundary-broker.md BB-D104, BB-D108):
// with no read grant it refuses a read and a write alike, naming the conversion a legacy JSONC
// user config needs, `yolo allow github read` and `yolo config apply`, and runs no gh.
func TestNoReadGrantRefusesEveryCall(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	f.b.scope = NewScope([]string{"o/r"})
	writeGHUserConfig(t, paths.UserConfigPath(), `{"packs": ["github"]}`)
	for _, argv := range [][]string{{"pr", "view", "32", "-R", "o/r"}, {"pr", "comment", "32", "-R", "o/r", "-b", "x"},
		{"auth", "status"}} {
		code, out, errOut := f.serve(t, Request{Argv: argv})
		if code != ExitUsage || out != "" {
			t.Fatalf("gh %v with no read grant: code %d out %q err %q", argv, code, out, errOut)
		}
		for _, want := range []string{"no read grant", "`yolo config migrate`", "`yolo allow github read`",
			"yolo config apply"} {
			if !strings.Contains(errOut, want) {
				t.Fatalf("gh %v refusal lacks %q:\n%s", argv, want, errOut)
			}
		}
		if ev := lastAudit(t); ev.Set != "refused" || ev.Reason != "no read grant" {
			t.Fatalf("gh %v audit %+v", argv, ev)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(f.fakeDir, "calls")); len(entries) != 0 {
		t.Fatal("gh ran with no read grant")
	}
}

// A TOML user (and one with no user config yet, which a first write makes TOML) is sent to the
// readable grant, `yolo allow github read` (the manifest's read_capability), never to a
// `brokered.github.grants` entry their config.toml cannot hold.
func TestNoReadGrantNamesYoloAllowToATOMLUser(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	f.b.scope = NewScope([]string{"o/r"})
	writeGHUserConfig(t, filepath.Join(filepath.Dir(paths.UserConfigPath()), paths.UserConfigTOMLName), `packs = ["github"]`)
	code, _, errOut := f.serve(t, Request{Argv: []string{"pr", "view", "32", "-R", "o/r"}})
	if code != ExitUsage || !strings.Contains(errOut, "no read grant") ||
		!strings.Contains(errOut, "`yolo allow github read`") || strings.Contains(errOut, "brokered.github.grants") {
		t.Fatalf("a TOML user's refusal does not name `yolo allow github read`: code %d\n%s", code, errOut)
	}
}

func writeGHUserConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A read grant naming one repository covers it, case-insensitively as GitHub names are, and no
// other repository in the scope.
func TestReadGrantCoversItsOwnRepository(t *testing.T) {
	f := newBrokerFixture(t, "2.101.0")
	f.b.scope = NewScope([]string{"o/r", "o/lib"}).WithGrants(brokerscope.Grant{Kind: brokerscope.KindRead,
		Repo: "github.com/O/R"})
	if code, out, errOut := f.serve(t, Request{Argv: []string{"pr", "view", "32", "-R", "o/r"}}); code != 0 {
		t.Fatalf("a read on the granted repository: code %d out %q err %q", code, out, errOut)
	}
	code, _, errOut := f.serve(t, Request{Argv: []string{"pr", "view", "32", "-R", "o/lib"}})
	if code != ExitUsage || !strings.Contains(errOut, "no read grant on github.com/o/lib") {
		t.Fatalf("a read on another repository: code %d err %q", code, errOut)
	}
}
