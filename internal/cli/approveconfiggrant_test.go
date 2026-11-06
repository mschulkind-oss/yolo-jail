package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/gitbroker"
)

// A branch declared in a TOML project's config: the --revoke refusal names the exact
// `yolo config unset` that removes it, and that command, run as printed, does.
func TestApproveRevokeOfAConfigBranchNamesTheUnsetVerb(t *testing.T) {
	fx := newNamedVerbFixture(t, "[requests.git]\npush = [\"feat\", \"main\"]\n", "[allow.git]\nalways = [\"fetch\"]\non_request = [\"push\"]\n")
	f := brokerscope.File{Source: gitbroker.Source, LaunchID: "cfgunset", Workspace: fx.workspace, Container: "c", PID: os.Getpid(),
		Form: brokerscope.FormRemoteURL, Repos: []string{"git@github.com:o/r.git"}, ConfigFile: "yolo-jail.jsonc",
		Grants: []brokerscope.Grant{{Kind: brokerscope.KindBranch, Repo: "*", Branch: "main"}}}
	path, err := brokerscope.Write(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := brokerscope.BindBroker(path, f, "start", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	listing := captureStdout(t, func() {
		if rc := registry["approve"]([]string{"approve"}); rc != 0 {
			t.Fatalf("list: %d", rc)
		}
	})
	var id string
	for _, l := range strings.Split(listing, "\n") {
		if strings.Contains(l, "refs/heads/main") && strings.Contains(l, "from config") {
			id = strings.Fields(l)[0]
			// The launch records the legacy name; the listing names the file the project holds.
			if !strings.Contains(l, fx.project) {
				t.Fatalf("the listing does not name the TOML file %s: %q", fx.project, l)
			}
		}
	}
	if id == "" {
		t.Fatalf("no config branch listed:\n%s", listing)
	}
	var rc int
	refusal := captureStderr(t, func() { rc = registry["approve"]([]string{"approve", "--revoke", id}) })
	t.Logf("revoke:\n%s", refusal)
	const unset = "yolo config unset 'requests.git.push[1]' --project"
	if rc != approveExitPolicy || !strings.Contains(refusal, unset) || !strings.Contains(refusal, "yolo config apply") ||
		strings.Contains(refusal, "brokered.git.grants") {
		t.Fatalf("revoke %s: rc %d\n%s", id, rc, refusal)
	}
	var out, errw bytes.Buffer
	if rc := runConfigEditWith([]string{"unset", "requests.git.push[1]", "--project"}, &out, &errw, fx.workspace); rc != 0 {
		t.Fatalf("the named verb refused: rc %d\n%s%s", rc, out.String(), errw.String())
	}
	if got := readFile(t, fx.project); strings.Contains(got, "main") || !strings.Contains(got, "feat") {
		t.Fatalf("the named verb did not remove main alone:\n%s", got)
	}
}
