package cli

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
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

// In a jail the user config is the host's to write: the verb names the command, quoted, for the
// human's host terminal, and writes nothing (the approval is the human's own answer).
func TestAllowReposInJailNamesTheQuotedHostCommand(t *testing.T) {
	fx := newNamedVerbFixture(t, "", "")
	t.Setenv("YOLO_VERSION", "9.9.9")
	if !config.InJail() {
		t.Skip("this fixture does not reach the in-jail reader")
	}
	userBefore := readFile(t, fx.userConfig)
	rc, out, errs := captureStd(t, func() int {
		return Main([]string{"yolo", "allow", "repos", "mine", "github.com/mschulkind/*"})
	})
	if rc == 0 {
		t.Fatalf("an in-jail allow repos wrote the user config: stdout=%q stderr=%q", out, errs)
	}
	if want := "yolo allow repos mine 'github.com/mschulkind/*'"; !strings.Contains(errs, want) {
		t.Fatalf("the in-jail step lacks the quoted command %q: %q", want, errs)
	}
	if readFile(t, fx.userConfig) != userBefore {
		t.Fatal("an in-jail allow repos edited the user config")
	}
}

// A repository's ceiling never holds the broker's read: the read is allowed for every repository by
// the broad `[allow.<broker>]` list alone (GW-D80). A scoped read is refused before anything is
// written, naming the broad allow; the broad read itself is unchanged.
func TestAllowReposRefusesAScopedReadWithoutWriting(t *testing.T) {
	fx := newNamedVerbFixture(t, "", "")
	recordAllowGate(t, false)
	if rc, out, errs := captureStd(t, func() int {
		return Main([]string{"yolo", "allow", "repos", "mine", "github.com/mschulkind/*"})
	}); rc != 0 {
		t.Fatalf("declaring the name rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	for _, args := range [][]string{
		{"yolo", "allow", "repos.mine.github", "always", "read"},
		{"yolo", "allow", "repos.mine.github", "on_request", "read"},
		{"yolo", "allow", "repos.mine.github", "read"},
	} {
		before := readFile(t, fx.userConfig) + readFile(t, fx.fragment)
		rc, out, errs := captureStd(t, func() int { return Main(args) })
		if rc != 1 {
			t.Fatalf("%v rc=%d, want 1; stdout=%q stderr=%q", args, rc, out, errs)
		}
		if !strings.Contains(errs, "yolo allow github always read") {
			t.Fatalf("%v refusal lacks the broad allow: %q", args, errs)
		}
		if after := readFile(t, fx.userConfig) + readFile(t, fx.fragment); after != before {
			t.Fatalf("%v was refused and still wrote the user config", args)
		}
	}
	rc, out, errs := captureStd(t, func() int { return Main([]string{"yolo", "allow", "github", "always", "read"}) })
	if rc != 0 {
		t.Fatalf("the broad read rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	if !strings.Contains(readFile(t, fx.userConfig)+readFile(t, fx.fragment), "[allow.github]") {
		t.Fatal("the broad read was not written")
	}
}
